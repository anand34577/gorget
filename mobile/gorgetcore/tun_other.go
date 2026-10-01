//go:build !linux

package gorgetcore

import (
	"errors"

	"golang.zx2c4.com/wireguard/tun"
)

func tunFromFD(int, int) (tun.Device, error) {
	return nil, errors.New("TUN file descriptors are only supported on Android/Linux")
}
