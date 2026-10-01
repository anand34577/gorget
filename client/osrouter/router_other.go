//go:build !windows && !darwin && !(linux && !android)

package osrouter

import (
	"errors"
	"log/slog"
)

func newRouter(*slog.Logger) (Router, error) {
	return nil, errors.New("the Gorget desktop client supports Linux, Windows and macOS")
}
