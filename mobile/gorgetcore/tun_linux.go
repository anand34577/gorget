//go:build linux

package gorgetcore

import "golang.zx2c4.com/wireguard/tun"

func tunFromFD(fd, _ int) (tun.Device, error) {
	dev, _, err := tun.CreateUnmonitoredTUNFromFD(fd)
	return dev, err
}
