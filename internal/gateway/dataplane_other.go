//go:build !linux && !darwin

package gateway

import (
	"errors"
	"log/slog"
	"runtime"

	"github.com/anand34577/gorget/internal/config"
)

func newDataplane(cfg config.GatewayConfig, log *slog.Logger) (dataplane, error) {
	return nil, errors.New("the WireGuard gateway runs on Linux and macOS (this is " + runtime.GOOS + ": it has no firewall that can enforce per-flow access rules); " +
		"the control plane, relay and STUN work on this OS, and standard WireGuard apps need a gateway on Linux or macOS")
}
