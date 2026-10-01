// Package rpcserver implements the ConnectRPC ControlService used by native
// Gorget clients.
package rpcserver

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	pb "github.com/anand34577/gorget/gen/gorget/v1"
	"github.com/anand34577/gorget/gen/gorget/v1/gorgetv1connect"
	"github.com/anand34577/gorget/internal/auth"
	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/relay"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

const (
	sessionTTL     = 24 * time.Hour
	challengeTTL   = 60 * time.Second
	loginTTL       = 10 * time.Minute
	keepalive      = 25 * time.Second
	maxSignalBytes = 4096
)

type Server struct {
	core   *core.Core
	log    *slog.Logger
	tokens tokenSigner
	// ClientIP extracts the real client IP (honours trusted proxies).
	ClientIP func(*http.Request) string

	unauthLimit *auth.Limiter
	deviceLimit *auth.Limiter
}

func New(c *core.Core, log *slog.Logger, clientIP func(*http.Request) string) *Server {
	return &Server{
		core:        c,
		log:         log,
		tokens:      tokenSigner{key: c.Box.DeriveKey("device-session")},
		ClientIP:    clientIP,
		unauthLimit: auth.NewLimiter(30, 20),
		deviceLimit: auth.NewLimiter(240, 60),
	}
}

// Handler returns the mount path and HTTP handler.
func (s *Server) Handler() (string, http.Handler) {
	return gorgetv1connect.NewControlServiceHandler(s,
		connect.WithInterceptors(&authInterceptor{s: s}),
		connect.WithReadMaxBytes(1<<20),
		connect.WithCompressMinBytes(1024),
	)
}

// ---------- auth interceptor ----------

type ctxKey struct{}

type deviceCtx struct {
	device *store.Device
	claims *tokenClaims
}

func deviceFrom(ctx context.Context) *store.Device {
	if v, ok := ctx.Value(ctxKey{}).(*deviceCtx); ok {
		return v.device
	}
	return nil
}

var publicProcedures = map[string]bool{
	gorgetv1connect.ControlServiceGetServerInfoProcedure:         true,
	gorgetv1connect.ControlServiceGetChallengeProcedure:          true,
	gorgetv1connect.ControlServiceStartInteractiveLoginProcedure: true,
	gorgetv1connect.ControlServicePollInteractiveLoginProcedure:  true,
	gorgetv1connect.ControlServiceRegisterProcedure:              true,
	gorgetv1connect.ControlServiceAuthenticateProcedure:          true,
}

type authInterceptor struct{ s *Server }

func (a *authInterceptor) authenticate(ctx context.Context, procedure string, h http.Header, peer connect.Peer) (context.Context, error) {
	if publicProcedures[procedure] {
		ip := peer.Addr
		if i := strings.LastIndex(ip, ":"); i > 0 {
			ip = ip[:i]
		}
		if fwd := h.Get("X-Gorget-Client-IP"); fwd != "" {
			ip = fwd // set by our HTTP middleware after trusted-proxy evaluation
		}
		if !a.s.unauthLimit.Allow(ip) {
			return ctx, connect.NewError(connect.CodeResourceExhausted, errors.New("rate limited"))
		}
		return ctx, nil
	}
	tok := strings.TrimPrefix(h.Get("Authorization"), "Bearer ")
	d, claims, err := a.s.verifyDevice(ctx, tok)
	if err != nil {
		return ctx, connect.NewError(connect.CodeUnauthenticated, err)
	}
	if !a.s.deviceLimit.Allow(d.ID) {
		return ctx, connect.NewError(connect.CodeResourceExhausted, errors.New("rate limited"))
	}
	return context.WithValue(ctx, ctxKey{}, &deviceCtx{device: d, claims: claims}), nil
}

func (a *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ctx, err := a.authenticate(ctx, req.Spec().Procedure, req.Header(), req.Peer())
		if err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (a *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (a *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx, err := a.authenticate(ctx, conn.Spec().Procedure, conn.RequestHeader(), conn.Peer())
		if err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

// verifyDevice validates a session token and the device's current state.
func (s *Server) verifyDevice(ctx context.Context, tok string) (*store.Device, *tokenClaims, error) {
	claims, err := s.tokens.verify(tok)
	if err != nil {
		return nil, nil, err
	}
	d, err := s.core.Store.GetDevice(ctx, claims.DeviceID)
	if err != nil {
		return nil, nil, errors.New("device not found")
	}
	if !d.MachineKey.Valid || d.MachineKey.String != claims.MachineKey {
		return nil, nil, errors.New("token does not match device")
	}
	if d.State == store.StateDisabled {
		return nil, nil, errors.New("device disabled")
	}
	if !d.KeyExpiryDisabled && d.KeyExpiresAt > 0 && time.Now().Unix() >= d.KeyExpiresAt {
		return nil, nil, errors.New("device key expired; log in again")
	}
	return d, claims, nil
}

// RelayAuthenticator lets the relay accept device session tokens.
func (s *Server) RelayAuthenticator() relay.Authenticator {
	return func(ctx context.Context, tok string) (relay.Identity, error) {
		d, _, err := s.verifyDevice(ctx, tok)
		if err != nil {
			return relay.Identity{}, err
		}
		if d.State != store.StateActive {
			return relay.Identity{}, errors.New("device not active")
		}
		raw, err := base64.StdEncoding.DecodeString(d.WGPublicKey)
		if err != nil {
			return relay.Identity{}, err
		}
		k, err := relay.ParseKey(raw)
		return relay.Identity{DeviceID: d.ID, Key: k}, err
	}
}

// RelayAuthorizer allows relaying only between peers permitted by policy.
func (s *Server) RelayAuthorizer() relay.Authorizer {
	return func(src string, dst relay.Key) bool {
		snap := s.core.Coord.Snapshot()
		dstID, ok := snap.ByWGKey[base64.StdEncoding.EncodeToString(dst[:])]
		if !ok || !snap.Active[src] || !snap.Active[dstID] {
			return false
		}
		return snap.Compiled.IsPeer(src, dstID)
	}
}

// hostParams converts the reported host details; unset fields stay zero.
func (s *Server) hostParams(h *pb.HostInfo, ip string) core.RegisterParams {
	p := core.RegisterParams{PublicIP: ip}
	if h != nil {
		p.Hostname, p.OS, p.OSVersion, p.Version, p.Arch = trunc(h.Hostname, 64), trunc(h.Os, 32), trunc(h.OsVersion, 64), trunc(h.ClientVersion, 32), trunc(h.Arch, 16)
		p.DiskEncrypted, p.FirewallOn = int(h.DiskEncrypted), int(h.FirewallEnabled)
	}
	return p
}

// peerIP is the address the request came from (X-Gorget-Client-IP is set by our HTTP
// layer after trusted-proxy handling; direct peers fall back to the socket address).
func peerIP(h http.Header, peer connect.Peer) string {
	if fwd := h.Get("X-Gorget-Client-IP"); fwd != "" {
		return fwd
	}
	ip := peer.Addr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	return ip
}

// ---------- unauthenticated RPCs ----------

func (s *Server) GetServerInfo(ctx context.Context, _ *connect.Request[pb.GetServerInfoRequest]) (*connect.Response[pb.GetServerInfoResponse], error) {
	resp := &pb.GetServerInfoResponse{
		ServerVersion:           core.Version,
		ProtocolVersion:         core.ProtocolVersion,
		MinProtocolVersion:      core.MinProtocolVersion,
		NetworkName:             s.core.Settings().Network.Name,
		InteractiveLoginEnabled: true,
	}
	if provs, err := s.core.Store.ListOIDCProviders(ctx); err == nil {
		for _, p := range provs {
			if p.Enabled {
				resp.SsoProviders = append(resp.SsoProviders, p.Name)
			}
		}
	}
	return connect.NewResponse(resp), nil
}

func decodeMachineKey(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("machine_key must be a base64 Ed25519 public key"))
	}
	return ed25519.PublicKey(b), nil
}

// challengeKey is the shared-state key of a login challenge (any instance can verify it).
func challengeKey(nonce string) string { return "dc:" + nonce }

func (s *Server) GetChallenge(ctx context.Context, req *connect.Request[pb.GetChallengeRequest]) (*connect.Response[pb.GetChallengeResponse], error) {
	if _, err := decodeMachineKey(req.Msg.MachineKey); err != nil {
		return nil, err
	}
	nonce := secrets.RandomToken("", 32)
	exp := time.Now().Add(challengeTTL)
	// Stored in the database (not in memory) so a load-balanced cluster works; the unauthenticated
	// rate limit bounds how many a single address can create.
	if err := s.core.Store.PutPending(ctx, challengeKey(nonce), req.Msg.MachineKey, int64(challengeTTL.Seconds())); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("could not create a challenge"))
	}
	return connect.NewResponse(&pb.GetChallengeResponse{Nonce: nonce, ExpiresAtUnix: exp.Unix()}), nil
}

// SignaturePayload is the message a client signs with its machine key.
func SignaturePayload(purpose, nonce, machineKey string) []byte {
	return []byte("gorget-v1|" + purpose + "|" + nonce + "|" + machineKey)
}

// verifyChallenge consumes a nonce and checks the signature.
func (s *Server) verifyChallenge(ctx context.Context, purpose, machineKey, nonce, sigB64 string) error {
	pub, err := decodeMachineKey(machineKey)
	if err != nil {
		return err
	}
	owner, ok, err := s.core.Store.TakePending(ctx, challengeKey(nonce)) // single use
	if err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("could not verify the challenge"))
	}
	if !ok || owner != machineKey {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("challenge expired or unknown; request a new one"))
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil || !ed25519.Verify(pub, SignaturePayload(purpose, nonce, machineKey), sig) {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid signature"))
	}
	return nil
}

func (s *Server) StartInteractiveLogin(ctx context.Context, req *connect.Request[pb.StartInteractiveLoginRequest]) (*connect.Response[pb.StartInteractiveLoginResponse], error) {
	if _, err := decodeMachineKey(req.Msg.MachineKey); err != nil {
		return nil, err
	}
	now := store.Now()
	l := &store.DeviceLogin{
		ID:         secrets.RandomID(),
		UserCode:   userCode(),
		MachineKey: req.Msg.MachineKey,
		Hostname:   trunc(req.Msg.Hostname, 64),
		OS:         trunc(req.Msg.Os, 32),
		Status:     "pending",
		CreatedAt:  now,
		ExpiresAt:  now + int64(loginTTL.Seconds()),
	}
	if err := s.core.Store.CreateDeviceLogin(ctx, l); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.StartInteractiveLoginResponse{
		LoginId:             l.ID,
		LoginUrl:            s.core.Cfg.PublicURL + "/device?code=" + l.UserCode,
		UserCode:            l.UserCode,
		ExpiresAtUnix:       l.ExpiresAt,
		PollIntervalSeconds: 2,
	}), nil
}

// userCode returns an 8-character code like "KXRT-4PQM" without ambiguous characters.
func userCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := secrets.RandomBytes(8)
	out := make([]byte, 0, 9)
	for i, x := range b {
		if i == 4 {
			out = append(out, '-')
		}
		out = append(out, alphabet[int(x)%len(alphabet)])
	}
	return string(out)
}

func (s *Server) PollInteractiveLogin(ctx context.Context, req *connect.Request[pb.PollInteractiveLoginRequest]) (*connect.Response[pb.PollInteractiveLoginResponse], error) {
	l, err := s.core.Store.GetDeviceLogin(ctx, req.Msg.LoginId)
	if err != nil || l.MachineKey != req.Msg.MachineKey {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("unknown login"))
	}
	resp := &pb.PollInteractiveLoginResponse{}
	switch {
	case store.Now() > l.ExpiresAt && l.Status == "pending":
		resp.Status = pb.LoginStatus_LOGIN_STATUS_EXPIRED
	case l.Status == "pending":
		resp.Status = pb.LoginStatus_LOGIN_STATUS_PENDING
	case l.Status == "denied":
		resp.Status = pb.LoginStatus_LOGIN_STATUS_DENIED
	case l.Status == "approved":
		tok := secrets.RandomToken("gl_", 32)
		l.LoginTokenHash = secrets.HashToken(tok)
		l.Status = "token_issued"
		l.ExpiresAt = store.Now() + 300
		if err := s.core.Store.UpdateDeviceLogin(ctx, l); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		resp.Status = pb.LoginStatus_LOGIN_STATUS_APPROVED
		resp.LoginToken = tok
	default:
		resp.Status = pb.LoginStatus_LOGIN_STATUS_EXPIRED
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) Register(ctx context.Context, req *connect.Request[pb.RegisterRequest]) (*connect.Response[pb.RegisterResponse], error) {
	m := req.Msg
	if err := s.verifyChallenge(ctx, "register", m.MachineKey, m.Nonce, m.Signature); err != nil {
		return nil, err
	}
	p := s.hostParams(m.Host, peerIP(req.Header(), req.Peer()))
	p.MachineKey, p.WGKey, p.DiscoKey, p.Name, p.Ephemeral = m.MachineKey, m.WireguardPublicKey, m.DiscoPublicKey, m.Name, m.Ephemeral
	actor := core.Actor{ID: "device", Name: "device:" + p.Hostname}
	switch {
	case m.SetupKey != "" && m.LoginToken != "":
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("use either a setup key or a login token"))
	case m.SetupKey != "":
		k, err := s.core.Store.ConsumeSetupKey(ctx, secrets.HashToken(strings.TrimSpace(m.SetupKey)))
		if err != nil {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("invalid, expired or used-up setup key"))
		}
		p.SetupKey = k
		actor.Name += " (setup key " + k.Name + ")"
	case m.LoginToken != "":
		l, err := s.core.Store.GetDeviceLoginByToken(ctx, secrets.HashToken(m.LoginToken))
		if err != nil || l.Status != "token_issued" || store.Now() > l.ExpiresAt || l.MachineKey != m.MachineKey {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("invalid or expired login token"))
		}
		u, err := s.core.Store.GetUser(ctx, l.UserID)
		if err != nil || u.Disabled {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("user not found or disabled"))
		}
		l.Status, l.LoginTokenHash = "consumed", ""
		if err := s.core.Store.UpdateDeviceLogin(ctx, l); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		p.User = u
		actor = core.Actor{ID: u.ID, Name: u.Email}
	default:
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("a setup key or login token is required"))
	}
	d, err := s.core.RegisterNative(ctx, p)
	if err != nil {
		return nil, toConnectErr(err)
	}
	s.core.Audit(ctx, actor, "device.register", "device", d.ID, d.Name, map[string]any{"os": d.OS, "state": d.State})
	snap := s.core.Coord.Snapshot()
	return connect.NewResponse(&pb.RegisterResponse{
		DeviceId: d.ID,
		State:    deviceState(d),
		Ipv4:     d.IPv4,
		Ipv6:     d.IPv6,
		Fqdn:     d.Name + "." + snap.Settings.Network.Domain,
	}), nil
}

func (s *Server) Authenticate(ctx context.Context, req *connect.Request[pb.AuthenticateRequest]) (*connect.Response[pb.AuthenticateResponse], error) {
	m := req.Msg
	if err := s.verifyChallenge(ctx, "auth", m.MachineKey, m.Nonce, m.Signature); err != nil {
		return nil, err
	}
	d, err := s.core.Store.GetDeviceByMachineKey(ctx, m.MachineKey)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("device not registered"))
	}
	resp := &pb.AuthenticateResponse{DeviceId: d.ID, State: deviceState(d), KeyExpiresAtUnix: d.KeyExpiresAt}
	if resp.State == pb.DeviceState_DEVICE_STATE_DISABLED || resp.State == pb.DeviceState_DEVICE_STATE_EXPIRED {
		return connect.NewResponse(resp), nil
	}
	changed := false
	if m.WireguardPublicKey != "" && m.WireguardPublicKey != d.WGPublicKey {
		if _, err := base64.StdEncoding.DecodeString(m.WireguardPublicKey); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid WireGuard key"))
		}
		d.WGPublicKey, changed = m.WireguardPublicKey, true
	}
	if m.DiscoPublicKey != "" && m.DiscoPublicKey != d.DiscoKey {
		d.DiscoKey, changed = m.DiscoPublicKey, true
	}
	if m.Host != nil {
		hp := s.hostParams(m.Host, peerIP(req.Header(), req.Peer()))
		if core.HostChanged(d, hp) {
			d.Hostname, d.OSVersion, d.ClientVersion, d.Arch = hp.Hostname, hp.OSVersion, hp.Version, hp.Arch
			d.DiskEncrypted, d.FirewallOn = hp.DiskEncrypted, hp.FirewallOn
			d.PublicIP = hp.PublicIP
			changed = true
		}
	}
	if changed {
		if err := s.core.Store.UpdateDevice(ctx, d); err != nil {
			if errors.Is(err, store.ErrConflict) {
				return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("WireGuard key already in use"))
			}
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		s.core.Coord.Trigger()
	}
	tok, exp := s.tokens.issue(d.ID, m.MachineKey, sessionTTL)
	if d.KeyExpiresAt > 0 && !d.KeyExpiryDisabled && exp > d.KeyExpiresAt {
		tok, exp = s.tokens.issue(d.ID, m.MachineKey, time.Until(time.Unix(d.KeyExpiresAt, 0)))
	}
	resp.SessionToken, resp.ExpiresAtUnix = tok, exp
	return connect.NewResponse(resp), nil
}

func deviceState(d *store.Device) pb.DeviceState {
	switch {
	case d.State == store.StateDisabled:
		return pb.DeviceState_DEVICE_STATE_DISABLED
	case d.State == store.StatePending:
		return pb.DeviceState_DEVICE_STATE_PENDING_APPROVAL
	case !d.KeyExpiryDisabled && d.KeyExpiresAt > 0 && time.Now().Unix() >= d.KeyExpiresAt:
		return pb.DeviceState_DEVICE_STATE_EXPIRED
	}
	return pb.DeviceState_DEVICE_STATE_ACTIVE
}

// ---------- authenticated RPCs ----------

func (s *Server) WatchNetworkMap(ctx context.Context, req *connect.Request[pb.WatchNetworkMapRequest], stream *connect.ServerStream[pb.WatchNetworkMapResponse]) error {
	d := deviceFrom(ctx)
	w, cancel := s.core.Coord.Watch(d.ID)
	defer cancel()

	var last *pb.NetworkMap
	var lastPosture string
	lastSerial := req.Msg.LastSerial
	ka := time.NewTicker(keepalive)
	defer ka.Stop()

	send := func() error {
		snap := s.core.Coord.Snapshot()
		nm := s.core.NetworkMap(snap, d.ID)
		if nm == nil && len(snap.Posture[d.ID]) > 0 {
			// Healthy device that breaks a posture rule: tell it what to fix and keep waiting.
			msg := "This device doesn't meet your organisation's security rules:\n- " + strings.Join(snap.Posture[d.ID], "\n- ")
			if msg != lastPosture {
				lastPosture = msg
				if err := stream.Send(&pb.WatchNetworkMapResponse{Update: &pb.WatchNetworkMapResponse_Notice{Notice: &pb.ServerNotice{Kind: pb.ServerNotice_KIND_POSTURE_FAILED, Message: msg}}}); err != nil {
					return err
				}
			}
			last = nil
			return nil
		}
		lastPosture = ""
		if nm == nil {
			cur, err := s.core.Store.GetDevice(ctx, d.ID)
			kind := pb.ServerNotice_KIND_DEVICE_DISABLED
			msg := "This device is not active."
			if err == nil {
				switch deviceState(cur) {
				case pb.DeviceState_DEVICE_STATE_ACTIVE:
					// Registered or re-enabled moments ago: the snapshot hasn't caught up yet.
					s.core.Coord.Trigger()
					return nil
				case pb.DeviceState_DEVICE_STATE_PENDING_APPROVAL:
					kind, msg = pb.ServerNotice_KIND_APPROVAL_PENDING, "Waiting for an administrator to approve this device."
				case pb.DeviceState_DEVICE_STATE_EXPIRED:
					kind, msg = pb.ServerNotice_KIND_KEY_EXPIRED, "Device key expired; please log in again."
				}
			}
			if err := stream.Send(&pb.WatchNetworkMapResponse{Update: &pb.WatchNetworkMapResponse_Notice{Notice: &pb.ServerNotice{Kind: kind, Message: msg}}}); err != nil {
				return err
			}
			if kind == pb.ServerNotice_KIND_APPROVAL_PENDING {
				last = nil
				return nil // keep waiting for approval
			}
			return errStreamDone
		}
		if last == nil {
			if lastSerial != 0 && lastSerial == nm.Serial {
				last = nm
				return nil
			}
			last = nm
			return stream.Send(&pb.WatchNetworkMapResponse{Update: &pb.WatchNetworkMapResponse_Full{Full: nm}})
		}
		delta := core.Diff(last, nm)
		last = nm
		if delta == nil {
			return nil
		}
		return stream.Send(&pb.WatchNetworkMapResponse{Update: &pb.WatchNetworkMapResponse_Delta{Delta: delta}})
	}

	if err := send(); err != nil {
		return streamErr(err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.Notify:
			if err := send(); err != nil {
				return streamErr(err)
			}
		case sig := <-w.Signals:
			err := stream.Send(&pb.WatchNetworkMapResponse{Update: &pb.WatchNetworkMapResponse_Signal{Signal: &pb.PeerSignal{
				FromPeerId: sig.FromID, FromDiscoKey: sig.FromDisco, Sealed: sig.Sealed,
			}}})
			if err != nil {
				return nil
			}
		case n := <-w.Notices:
			kind := map[string]pb.ServerNotice_Kind{
				"disabled":   pb.ServerNotice_KIND_DEVICE_DISABLED,
				"expired":    pb.ServerNotice_KIND_KEY_EXPIRED,
				"logged_out": pb.ServerNotice_KIND_LOGGED_OUT,
				"pending":    pb.ServerNotice_KIND_APPROVAL_PENDING,
			}[n.Kind]
			if kind == 0 {
				kind = pb.ServerNotice_KIND_MESSAGE
			}
			_ = stream.Send(&pb.WatchNetworkMapResponse{Update: &pb.WatchNetworkMapResponse_Notice{Notice: &pb.ServerNotice{Kind: kind, Message: n.Message}}})
			if kind != pb.ServerNotice_KIND_MESSAGE {
				return nil
			}
		case <-ka.C:
			if err := stream.Send(&pb.WatchNetworkMapResponse{Update: &pb.WatchNetworkMapResponse_Keepalive{Keepalive: &pb.KeepAlive{ServerTimeUnix: time.Now().Unix()}}}); err != nil {
				return nil
			}
		}
	}
}

var errStreamDone = errors.New("stream done")

func streamErr(err error) error {
	if errors.Is(err, errStreamDone) {
		return nil
	}
	return err
}

func (s *Server) UpdateStatus(ctx context.Context, req *connect.Request[pb.UpdateStatusRequest]) (*connect.Response[pb.UpdateStatusResponse], error) {
	d := deviceFrom(ctx)
	m := req.Msg
	var host *core.RegisterParams
	if m.Host != nil {
		hp := s.hostParams(m.Host, peerIP(req.Header(), req.Peer()))
		host = &hp
	}
	if err := s.core.UpdateStatus(ctx, d, m.Endpoints, trunc(m.HomeRelay, 64), m.AdvertisedRoutes, m.AdvertiseExitNode, host); err != nil {
		return nil, toConnectErr(err)
	}
	var rx, tx uint64
	for _, p := range m.Peers {
		rx += p.RxBytes
		tx += p.TxBytes
	}
	if rx > 0 || tx > 0 {
		ep := ""
		if len(m.Endpoints) > 0 {
			ep = m.Endpoints[0]
		}
		_ = s.core.Store.TouchDevice(ctx, d.ID, store.Now(), ep, int64(rx), int64(tx))
	}
	return connect.NewResponse(&pb.UpdateStatusResponse{}), nil
}

func (s *Server) SendSignal(ctx context.Context, req *connect.Request[pb.SendSignalRequest]) (*connect.Response[pb.SendSignalResponse], error) {
	d := deviceFrom(ctx)
	if len(req.Msg.Sealed) == 0 || len(req.Msg.Sealed) > maxSignalBytes {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("signal size out of range"))
	}
	snap := s.core.Coord.Snapshot()
	if !snap.Active[d.ID] || !snap.Compiled.IsPeer(d.ID, req.Msg.ToPeerId) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("not a peer"))
	}
	if !s.core.Coord.SendSignal(d, req.Msg.ToPeerId, req.Msg.Sealed) {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("peer is offline"))
	}
	return connect.NewResponse(&pb.SendSignalResponse{}), nil
}

func (s *Server) Logout(ctx context.Context, _ *connect.Request[pb.LogoutRequest]) (*connect.Response[pb.LogoutResponse], error) {
	d := deviceFrom(ctx)
	actor := core.Actor{ID: "device", Name: "device:" + d.Name}
	if d.Ephemeral {
		if err := s.core.DeleteDevice(ctx, actor, d.ID); err != nil {
			return nil, toConnectErr(err)
		}
	} else if err := s.core.ExpireDeviceKey(ctx, actor, d.ID); err != nil {
		return nil, toConnectErr(err)
	}
	return connect.NewResponse(&pb.LogoutResponse{}), nil
}

func toConnectErr(err error) error {
	var ie *core.InvalidError
	switch {
	case errors.As(err, &ie):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrConflict):
		return connect.NewError(connect.CodeAlreadyExists, err)
	}
	return connect.NewError(connect.CodeInternal, fmt.Errorf("internal error"))
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
