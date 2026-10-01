//go:build !linux

package magicsock

import (
	"net"

	"golang.org/x/net/ipv4"
)

// Batched system calls (recvmmsg/sendmmsg) exist only on Linux; elsewhere one packet per call.
const batchSize = 1

type batchConn struct {
	read  func([]ipv4.Message, int) (int, error)
	write func([]ipv4.Message, int) (int, error)
}

func newBatchConn(*net.UDPConn, bool) *batchConn { return nil }
