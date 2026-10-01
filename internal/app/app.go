// Package app wires every server component together.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"

	"github.com/caddyserver/certmagic"

	"github.com/anand34577/gorget/internal/api"
	"github.com/anand34577/gorget/internal/auth"
	"github.com/anand34577/gorget/internal/backup"
	"github.com/anand34577/gorget/internal/config"
	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/gateway"
	"github.com/anand34577/gorget/internal/ha"
	"github.com/anand34577/gorget/internal/metrics"
	"github.com/anand34577/gorget/internal/relay"
	"github.com/anand34577/gorget/internal/rpcserver"
	"github.com/anand34577/gorget/internal/scim"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
	"github.com/anand34577/gorget/internal/stun"
	"github.com/anand34577/gorget/internal/web"
)

// OpenStore loads the master key and opens the database.
func OpenStore(ctx context.Context, cfg config.Config) (*store.Store, *secrets.Box, error) {
	key, err := secrets.LoadOrCreateMasterKey(cfg.Security.MasterKey, cfg.Security.MasterKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("master key: %w", err)
	}
	box, err := secrets.NewBox(key)
	if err != nil {
		return nil, nil, err
	}
	st, err := store.Open(ctx, cfg.Database.Driver, cfg.Database.DSN, cfg.Database.MaxOpenConns)
	if err != nil {
		return nil, nil, err
	}
	return st, box, nil
}

// relayAdvertise is the WebSocket relay URL clients use for this instance (empty when the relay is off).
func relayAdvertise(cfg config.Config) string {
	if !cfg.Relay.Enabled {
		return ""
	}
	return cfg.Cluster.RelayURL
}

func udpAdvertise(cfg config.Config) string {
	if !cfg.Relay.Enabled || cfg.Relay.UDPListen == "" {
		return ""
	}
	return cfg.Relay.UDPAdvertise
}

type sysInfo struct {
	tls   *web.TLSManager
	relay *relay.Server
	stun  *stun.Server
}

func (s sysInfo) TLSStatus() any { return s.tls.Status() }
func (s sysInfo) RelayStats() any {
	if s.relay == nil {
		return map[string]any{"enabled": false}
	}
	return map[string]any{"enabled": true, "connections": s.relay.Connections.Load(), "udp_sessions": s.relay.UDPSessions.Load(), "bytes": s.relay.BytesRelayed.Load(), "dropped": s.relay.PacketsDropped.Load()}
}
func (s sysInfo) STUNStats() any {
	if s.stun == nil {
		return map[string]any{"enabled": false}
	}
	return map[string]any{"enabled": true, "requests": s.stun.Requests.Load()}
}

// Run starts the server and blocks until ctx is cancelled.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	st, box, err := OpenStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	log.Info("database ready", "driver", cfg.Database.Driver)

	c, err := core.New(ctx, cfg, st, box, log)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Several instances on one PostgreSQL database: shared presence, notifications, certificates, relays.
	var certStorage certmagic.Storage
	var clusterRelay *ha.Cluster
	if cfg.Cluster.Enabled {
		cl := ha.New(ha.Options{
			DSN: cfg.Database.DSN, Store: st, Core: c, Log: log.With("component", "cluster"),
			InstanceID: cfg.Cluster.InstanceID, Region: cfg.Relay.Region, Name: cfg.Cluster.InstanceID,
			RelayURL: relayAdvertise(cfg), UDPAddr: udpAdvertise(cfg),
			PeerListen: cfg.Cluster.PeerListen, PeerAddr: cfg.Cluster.PeerAddr,
			MACKey: box.DeriveKey("cluster-relay-link"),
		})
		c.SetCluster(cl)
		clusterRelay = cl
		certStorage = ha.NewCertStorage(st, cfg.Cluster.InstanceID)
		log.Info("cluster mode", "instance", cfg.Cluster.InstanceID, "region", cfg.Relay.Region)
	}
	c.Run(ctx)

	ws, err := web.New(cfg, log, certStorage)
	if err != nil {
		return err
	}
	ws.OverlayPrefixes = func() []netip.Prefix {
		p := c.Plan()
		return []netip.Prefix{p.IPv4, p.IPv6}
	}

	am := auth.New(c)
	gw := gateway.New(c, log.With("component", "gateway"))
	rpc := rpcserver.New(c, log.With("component", "rpc"), ws.ClientIP)

	var rl *relay.Server
	if cfg.Relay.Enabled {
		rl = relay.New(log.With("component", "relay"), rpc.RelayAuthenticator(), rpc.RelayAuthorizer(), cfg.Relay.RateLimitBytes)
		if clusterRelay != nil {
			rl.SetRemote(clusterRelay)
			clusterRelay.SetRelay(rl)
		}
	}
	if clusterRelay != nil {
		go clusterRelay.Run(ctx) // after the relay exists: it forwards packets between instances
	}
	var stunSrv *stun.Server
	if cfg.STUN.Enabled {
		stunSrv = stun.New(log.With("component", "stun"))
	}

	apiH := api.New(c, am, gw, sysInfo{tls: ws.TLS, relay: rl, stun: stunSrv}, log.With("component", "api"), ws.ClientIP)
	rpcPath, rpcHandler := rpc.Handler()
	routes := web.Routes{
		API:     apiH.Routes(),
		SCIM:    scim.New(c, cfg.PublicURL).Routes(),
		RPCPath: rpcPath,
		RPC:     rpcHandler,
		Ready:   st.Ping,
	}
	if rl != nil {
		routes.Relay = rl
	}

	var wg sync.WaitGroup
	errc := make(chan error, 4)
	start := func(name string, f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
				errc <- fmt.Errorf("%s: %w", name, err)
			}
		}()
	}
	start("web", func() error { return ws.Serve(ctx, ws.Handler(routes)) })
	if stunSrv != nil {
		start("stun", func() error { return stunSrv.ListenAndServe(ctx, cfg.STUN.Listen) })
	}
	if rl != nil && cfg.Relay.UDPListen != "" {
		udp := rl.NewUDP()
		start("udp-relay", func() error { return udp.Serve(ctx, cfg.Relay.UDPListen) })
	}
	if cfg.Gateway.Enabled {
		start("gateway", func() error { gw.Run(ctx); return nil })
	}
	if cfg.Metrics.Enabled {
		reg := metrics.Registry(metrics.Sources{Core: c, Relay: rl, STUN: stunSrv})
		start("metrics", func() error { metrics.Serve(ctx, cfg.Metrics.Listen, cfg.Metrics.Token, reg, log); return nil })
	}
	if cfg.Database.Driver == "sqlite" || cfg.Backup.Interval > 0 {
		start("backup", func() error {
			backup.Scheduler(ctx, st, box, cfg.Backup.Dir, cfg.Backup.Interval, cfg.Backup.Keep, log.With("component", "backup"))
			return nil
		})
	}
	log.Info("gorget-server started", "version", core.Version, "public_url", cfg.PublicURL)

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errc:
		log.Error("component failed; shutting down", "err", runErr)
	}
	cancel()
	wg.Wait()
	log.Info("gorget-server stopped")
	return runErr
}
