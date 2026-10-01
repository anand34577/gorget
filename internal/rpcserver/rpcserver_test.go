package rpcserver

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	pb "github.com/anand34577/gorget/gen/gorget/v1"
	"github.com/anand34577/gorget/gen/gorget/v1/gorgetv1connect"
	"github.com/anand34577/gorget/internal/config"
	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

type env struct {
	core   *core.Core
	client gorgetv1connect.ControlServiceClient
	keyRaw string
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := config.Default()
	cfg.PublicURL = "https://vpn.example.com"
	cfg.DataDir = dir
	cfg.TLS.Mode = config.TLSModeOff
	if err := cfg.Finalize(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, "sqlite", filepath.Join(dir, "test.db"), 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	box, _ := secrets.NewBox(secrets.RandomBytes(32))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := core.New(ctx, cfg, st, box, log)
	if err != nil {
		t.Fatal(err)
	}
	c.Run(ctx)
	srv := New(c, log, func(r *http.Request) string { return "127.0.0.1" })
	path, h := srv.Handler()
	mux := http.NewServeMux()
	mux.Handle(path, h)
	hs := httptest.NewUnstartedServer(mux)
	hs.EnableHTTP2 = true
	hs.StartTLS()
	t.Cleanup(hs.Close)

	plain := secrets.RandomToken("gsk_", 24)
	k := &store.SetupKey{ID: secrets.RandomID(), Name: "test", KeyHash: secrets.HashToken(plain), KeyPrefix: plain[:10], Reusable: true, AutoApprove: true, Tags: store.StringList{}, CreatedAt: store.Now()}
	if err := st.CreateSetupKey(ctx, k); err != nil {
		t.Fatal(err)
	}
	return &env{core: c, client: gorgetv1connect.NewControlServiceClient(hs.Client(), hs.URL), keyRaw: plain}
}

type device struct {
	mk    ed25519.PrivateKey
	mkPub string
	wg    wgtypes.Key
	disco wgtypes.Key
	id    string
	token string
}

func newDevice() *device {
	pub, priv, _ := ed25519.GenerateKey(nil)
	wg, _ := wgtypes.GeneratePrivateKey()
	disco, _ := wgtypes.GeneratePrivateKey()
	return &device{mk: priv, mkPub: base64.StdEncoding.EncodeToString(pub), wg: wg, disco: disco}
}

func (e *env) sign(t *testing.T, d *device, purpose string) (string, string) {
	t.Helper()
	ch, err := e.client.GetChallenge(context.Background(), connect.NewRequest(&pb.GetChallengeRequest{MachineKey: d.mkPub}))
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(d.mk, SignaturePayload(purpose, ch.Msg.Nonce, d.mkPub))
	return ch.Msg.Nonce, base64.StdEncoding.EncodeToString(sig)
}

func (e *env) register(t *testing.T, d *device, name string) {
	t.Helper()
	nonce, sig := e.sign(t, d, "register")
	resp, err := e.client.Register(context.Background(), connect.NewRequest(&pb.RegisterRequest{
		MachineKey: d.mkPub, Nonce: nonce, Signature: sig, SetupKey: e.keyRaw,
		WireguardPublicKey: d.wg.PublicKey().String(), DiscoPublicKey: d.disco.PublicKey().String(),
		Host: &pb.HostInfo{Hostname: name, Os: "linux", ClientVersion: "test"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Msg.State != pb.DeviceState_DEVICE_STATE_ACTIVE {
		t.Fatalf("state %v", resp.Msg.State)
	}
	d.id = resp.Msg.DeviceId
	nonce, sig = e.sign(t, d, "auth")
	auth, err := e.client.Authenticate(context.Background(), connect.NewRequest(&pb.AuthenticateRequest{MachineKey: d.mkPub, Nonce: nonce, Signature: sig}))
	if err != nil {
		t.Fatal(err)
	}
	d.token = auth.Msg.SessionToken
}

func authed[T any](d *device, msg *T) *connect.Request[T] {
	r := connect.NewRequest(msg)
	r.Header().Set("Authorization", "Bearer "+d.token)
	return r
}

func TestNativeClientFlow(t *testing.T) {
	e := setup(t)
	a, b := newDevice(), newDevice()
	e.register(t, a, "alpha")
	e.register(t, b, "bravo")

	// Replayed nonce must fail.
	nonce, sig := e.sign(t, a, "auth")
	if _, err := e.client.Authenticate(context.Background(), connect.NewRequest(&pb.AuthenticateRequest{MachineKey: a.mkPub, Nonce: nonce, Signature: sig})); err != nil {
		t.Fatal(err)
	}
	if _, err := e.client.Authenticate(context.Background(), connect.NewRequest(&pb.AuthenticateRequest{MachineKey: a.mkPub, Nonce: nonce, Signature: sig})); err == nil {
		t.Fatal("replayed nonce accepted")
	}
	// Signature from another key must fail.
	nonce, _ = e.sign(t, a, "auth")
	badSig := base64.StdEncoding.EncodeToString(ed25519.Sign(b.mk, SignaturePayload("auth", nonce, a.mkPub)))
	if _, err := e.client.Authenticate(context.Background(), connect.NewRequest(&pb.AuthenticateRequest{MachineKey: a.mkPub, Nonce: nonce, Signature: badSig})); err == nil {
		t.Fatal("forged signature accepted")
	}
	// Unauthenticated status update must fail.
	if _, err := e.client.UpdateStatus(context.Background(), connect.NewRequest(&pb.UpdateStatusRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("expected unauthenticated, got %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stream, err := e.client.WatchNetworkMap(ctx, authed(a, &pb.WatchNetworkMapRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	next := func() *pb.WatchNetworkMapResponse {
		for stream.Receive() {
			if stream.Msg().GetKeepalive() == nil {
				return stream.Msg()
			}
		}
		t.Fatalf("stream ended: %v", stream.Err())
		return nil
	}
	full := next().GetFull()
	if full == nil {
		t.Fatal("expected full map first")
	}
	found := false
	for _, p := range full.Peers {
		if p.Id == b.id && p.WireguardPublicKey == b.wg.PublicKey().String() {
			found = true
		}
	}
	if !found {
		t.Fatalf("bravo missing from alpha's peers: %+v", full.Peers)
	}
	if full.Self.Fqdn != "alpha.gorget.internal" || full.Dns.Domain != "gorget.internal" {
		t.Fatalf("self/dns wrong: %+v %+v", full.Self, full.Dns)
	}

	// Bravo reports endpoints -> alpha gets a delta with bravo's endpoint.
	if _, err := e.client.UpdateStatus(context.Background(), authed(b, &pb.UpdateStatusRequest{Endpoints: []string{"203.0.113.9:41641"}, AdvertisedRoutes: []string{"192.168.50.0/24"}})); err != nil {
		t.Fatal(err)
	}
	for {
		m := next()
		if d := m.GetDelta(); d != nil {
			ok := false
			for _, p := range d.PeersUpserted {
				if p.Id == b.id && len(p.Endpoints) == 1 && p.Endpoints[0] == "203.0.113.9:41641" {
					ok = true
				}
			}
			if ok {
				break
			}
		}
	}

	// Signalling between peers.
	if _, err := e.client.SendSignal(context.Background(), authed(b, &pb.SendSignalRequest{ToPeerId: a.id, Sealed: []byte("hello")})); err != nil {
		t.Fatal(err)
	}
	for {
		m := next()
		if s := m.GetSignal(); s != nil {
			if s.FromPeerId != b.id || string(s.Sealed) != "hello" {
				t.Fatalf("bad signal %+v", s)
			}
			break
		}
	}

	// Disabling alpha ends its stream with a notice.
	disabled := store.StateDisabled
	if _, err := e.core.UpdateDevice(context.Background(), core.SystemActor, a.id, core.DeviceUpdate{State: &disabled}); err != nil {
		t.Fatal(err)
	}
	for {
		m := next()
		if n := m.GetNotice(); n != nil {
			if n.Kind != pb.ServerNotice_KIND_DEVICE_DISABLED {
				t.Fatalf("notice %v", n.Kind)
			}
			break
		}
	}
	if _, err := e.client.UpdateStatus(context.Background(), authed(a, &pb.UpdateStatusRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("disabled device still authorised: %v", err)
	}
}
