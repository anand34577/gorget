// Package metrics exposes opt-in Prometheus metrics on a separate listener.
// Nothing is ever sent anywhere; scraping is pull-only by the operator.
package metrics

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/anand34577/gorget/internal/core"
	"github.com/anand34577/gorget/internal/dnsserver"
	"github.com/anand34577/gorget/internal/relay"
	"github.com/anand34577/gorget/internal/store"
	"github.com/anand34577/gorget/internal/stun"
)

type Sources struct {
	Core  *core.Core
	Relay *relay.Server
	STUN  *stun.Server
	DNS   *dnsserver.Server
}

func Registry(src Sources) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	gauge := func(name, help string, f func() float64) {
		reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: "gorget", Name: name, Help: help}, f))
	}
	counter := func(name, help string, f func() float64) {
		reg.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: "gorget", Name: name, Help: help}, f))
	}
	devices := func(filter func(*store.Device) bool) func() float64 {
		return func() float64 {
			n := 0
			for _, d := range src.Core.Coord.Snapshot().Devices {
				if filter(d) {
					n++
				}
			}
			return float64(n)
		}
	}
	gauge("devices_total", "Registered devices (all kinds).", devices(func(*store.Device) bool { return true }))
	gauge("devices_pending", "Devices awaiting approval.", devices(func(d *store.Device) bool { return d.State == store.StatePending }))
	gauge("devices_online", "Connected native devices.", func() float64 { return float64(src.Core.Coord.OnlineCount()) })
	gauge("policy_version", "Active policy version.", func() float64 { return float64(src.Core.Coord.Snapshot().PolicyVer) })
	gauge("netmap_serial", "Network state serial.", func() float64 { return float64(src.Core.Coord.Snapshot().Serial) })
	if src.Relay != nil {
		gauge("relay_connections", "Active relay connections.", func() float64 { return float64(src.Relay.Connections.Load()) })
		gauge("relay_udp_sessions", "Active UDP relay sessions.", func() float64 { return float64(src.Relay.UDPSessions.Load()) })
		counter("relay_bytes_total", "Bytes forwarded by the relay.", func() float64 { return float64(src.Relay.BytesRelayed.Load()) })
		counter("relay_dropped_packets_total", "Packets dropped by the relay.", func() float64 { return float64(src.Relay.PacketsDropped.Load()) })
	}
	if src.STUN != nil {
		counter("stun_requests_total", "STUN binding requests answered.", func() float64 { return float64(src.STUN.Requests.Load()) })
	}
	return reg
}

// Serve runs the metrics listener with optional bearer-token protection.
func Serve(ctx context.Context, addr, token string, reg *prometheus.Registry, log *slog.Logger) {
	h := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	mux := http.NewServeMux()
	mux.Handle("/metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			got := r.Header.Get("Authorization")
			if subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+token)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		h.ServeHTTP(w, r)
	}))
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("metrics listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("metrics listener failed", "err", err)
	}
}
