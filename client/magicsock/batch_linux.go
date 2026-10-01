//go:build linux

package magicsock

import (
	"net"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// batchSize is how many packets one receive or send call may handle (recvmmsg/sendmmsg).
const batchSize = 32

// batchConn sends and receives several datagrams per system call.
type batchConn struct {
	read  func([]ipv4.Message, int) (int, error)
	write func([]ipv4.Message, int) (int, error)
}

func newBatchConn(uc *net.UDPConn, v6 bool) *batchConn {
	if v6 {
		p := ipv6.NewPacketConn(uc)
		return &batchConn{
			read:  func(ms []ipv4.Message, f int) (int, error) { return p.ReadBatch(ms, f) },
			write: func(ms []ipv4.Message, f int) (int, error) { return p.WriteBatch(ms, f) },
		}
	}
	p := ipv4.NewPacketConn(uc)
	return &batchConn{
		read:  func(ms []ipv4.Message, f int) (int, error) { return p.ReadBatch(ms, f) },
		write: func(ms []ipv4.Message, f int) (int, error) { return p.WriteBatch(ms, f) },
	}
}
