//go:build tools

package gorgetcore

// gomobile bind needs golang.org/x/mobile/bind in this module's dependencies.
// This import keeps `go mod tidy` from removing it.
import _ "golang.org/x/mobile/bind"
