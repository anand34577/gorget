package client

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	pb "github.com/anand34577/gorget/gen/gorget/v1"
	"github.com/anand34577/gorget/gen/gorget/v1/gorgetv1connect"
)

// control talks to the coordination server.
type control struct {
	url    string
	hc     *http.Client
	rpc    gorgetv1connect.ControlServiceClient
	mk     ed25519.PrivateKey
	mkPub  string
	mu     sync.Mutex
	token  string
	expiry time.Time
}

func newHTTPClient(d *net.Dialer, tlsConf *tls.Config) *http.Client {
	tr := &http.Transport{
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       tlsConf,
		TLSHandshakeTimeout:   10 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   4,
		ResponseHeaderTimeout: 0, // streams are long-lived
	}
	return &http.Client{Transport: tr}
}

func newControl(url string, hc *http.Client, mk ed25519.PrivateKey) *control {
	url = strings.TrimRight(url, "/")
	return &control{
		url:   url,
		hc:    hc,
		rpc:   gorgetv1connect.NewControlServiceClient(hc, url),
		mk:    mk,
		mkPub: base64.StdEncoding.EncodeToString(mk.Public().(ed25519.PublicKey)),
	}
}

func (c *control) sign(ctx context.Context, purpose string) (nonce, sig string, err error) {
	ch, err := c.rpc.GetChallenge(ctx, connect.NewRequest(&pb.GetChallengeRequest{MachineKey: c.mkPub}))
	if err != nil {
		return "", "", err
	}
	payload := []byte("gorget-v1|" + purpose + "|" + ch.Msg.Nonce + "|" + c.mkPub)
	return ch.Msg.Nonce, base64.StdEncoding.EncodeToString(ed25519.Sign(c.mk, payload)), nil
}

func (c *control) serverInfo(ctx context.Context) (*pb.GetServerInfoResponse, error) {
	r, err := c.rpc.GetServerInfo(ctx, connect.NewRequest(&pb.GetServerInfoRequest{}))
	if err != nil {
		return nil, err
	}
	return r.Msg, nil
}

func (c *control) register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	nonce, sig, err := c.sign(ctx, "register")
	if err != nil {
		return nil, err
	}
	req.MachineKey, req.Nonce, req.Signature = c.mkPub, nonce, sig
	r, err := c.rpc.Register(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return r.Msg, nil
}

func (c *control) authenticate(ctx context.Context, wgPub, discoPub string, host *pb.HostInfo) (*pb.AuthenticateResponse, error) {
	nonce, sig, err := c.sign(ctx, "auth")
	if err != nil {
		return nil, err
	}
	r, err := c.rpc.Authenticate(ctx, connect.NewRequest(&pb.AuthenticateRequest{
		MachineKey: c.mkPub, Nonce: nonce, Signature: sig, WireguardPublicKey: wgPub, DiscoPublicKey: discoPub, Host: host,
	}))
	if err != nil {
		return nil, err
	}
	if r.Msg.SessionToken != "" {
		c.mu.Lock()
		c.token, c.expiry = r.Msg.SessionToken, time.Unix(r.Msg.ExpiresAtUnix, 0)
		c.mu.Unlock()
	}
	return r.Msg, nil
}

// Token returns the current session token ("" if none or about to expire).
func (c *control) Token() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Until(c.expiry) < time.Minute {
		return ""
	}
	return c.token
}

func (c *control) clearToken() {
	c.mu.Lock()
	c.token, c.expiry = "", time.Time{}
	c.mu.Unlock()
}

func authed[T any](tok string, msg *T) *connect.Request[T] {
	r := connect.NewRequest(msg)
	r.Header().Set("Authorization", "Bearer "+tok)
	return r
}

var errNoToken = errors.New("not authenticated")

func (c *control) watch(ctx context.Context, lastSerial uint64, fn func(*pb.WatchNetworkMapResponse)) error {
	tok := c.Token()
	if tok == "" {
		return errNoToken
	}
	stream, err := c.rpc.WatchNetworkMap(ctx, authed(tok, &pb.WatchNetworkMapRequest{LastSerial: lastSerial}))
	if err != nil {
		return err
	}
	defer stream.Close()
	for stream.Receive() {
		fn(stream.Msg())
	}
	if err := stream.Err(); err != nil {
		return err
	}
	return errors.New("stream closed by server")
}

func (c *control) updateStatus(ctx context.Context, req *pb.UpdateStatusRequest) error {
	tok := c.Token()
	if tok == "" {
		return errNoToken
	}
	_, err := c.rpc.UpdateStatus(ctx, authed(tok, req))
	return err
}

func (c *control) sendSignal(ctx context.Context, peerID string, sealed []byte) error {
	tok := c.Token()
	if tok == "" {
		return errNoToken
	}
	_, err := c.rpc.SendSignal(ctx, authed(tok, &pb.SendSignalRequest{ToPeerId: peerID, Sealed: sealed}))
	return err
}

func (c *control) logout(ctx context.Context) error {
	tok := c.Token()
	if tok == "" {
		return errNoToken
	}
	_, err := c.rpc.Logout(ctx, authed(tok, &pb.LogoutRequest{}))
	return err
}

// friendly converts RPC errors into short user-facing messages.
func friendly(err error) string {
	if err == nil {
		return ""
	}
	var ce *connect.Error
	if errors.As(err, &ce) {
		switch ce.Code() {
		case connect.CodeUnavailable:
			return "Can't reach the server. Check your connection and the server address."
		case connect.CodePermissionDenied, connect.CodeUnauthenticated:
			return ce.Message()
		case connect.CodeInvalidArgument, connect.CodeNotFound, connect.CodeAlreadyExists:
			return ce.Message()
		}
		return fmt.Sprintf("%s (%s)", ce.Message(), ce.Code())
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return "Can't reach the server. Check your connection and the server address."
	}
	return err.Error()
}
