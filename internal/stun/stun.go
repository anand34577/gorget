// Package stun implements a minimal RFC 5389 STUN server answering Binding
// requests with the client's reflexive address (XOR-MAPPED-ADDRESS).
package stun

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
)

const (
	magicCookie     = 0x2112A442
	bindingRequest  = 0x0001
	bindingSuccess  = 0x0101
	attrXorMapped   = 0x0020
	attrSoftware    = 0x8022
	attrFingerprint = 0x8028
	headerLen       = 20
)

type Server struct {
	log      *slog.Logger
	Requests atomic.Uint64
}

func New(log *slog.Logger) *Server { return &Server{log: log} }

// ListenAndServe serves until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		pc.Close()
	}()
	s.log.Info("STUN server listening", "addr", pc.LocalAddr().String())
	buf := make([]byte, 1500)
	for {
		n, raddr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			continue
		}
		ua, ok := raddr.(*net.UDPAddr)
		if !ok {
			continue
		}
		resp, ok := Response(buf[:n], ua.AddrPort())
		if !ok {
			continue
		}
		s.Requests.Add(1)
		_, _ = pc.WriteTo(resp, raddr)
	}
}

// Response builds a Binding success response for a valid Binding request.
func Response(req []byte, from netip.AddrPort) ([]byte, bool) {
	if len(req) < headerLen || req[0]&0xC0 != 0 {
		return nil, false
	}
	if binary.BigEndian.Uint16(req[0:2]) != bindingRequest || binary.BigEndian.Uint32(req[4:8]) != magicCookie {
		return nil, false
	}
	if int(binary.BigEndian.Uint16(req[2:4]))+headerLen > len(req) {
		return nil, false
	}
	txID := req[8:20]

	addr := from.Addr().Unmap()
	var attr []byte
	port := from.Port() ^ uint16(magicCookie>>16)
	if addr.Is4() {
		attr = make([]byte, 4+8)
		binary.BigEndian.PutUint16(attr[0:2], attrXorMapped)
		binary.BigEndian.PutUint16(attr[2:4], 8)
		attr[5] = 0x01
		binary.BigEndian.PutUint16(attr[6:8], port)
		a := addr.As4()
		x := binary.BigEndian.Uint32(a[:]) ^ magicCookie
		binary.BigEndian.PutUint32(attr[8:12], x)
	} else {
		attr = make([]byte, 4+20)
		binary.BigEndian.PutUint16(attr[0:2], attrXorMapped)
		binary.BigEndian.PutUint16(attr[2:4], 20)
		attr[5] = 0x02
		binary.BigEndian.PutUint16(attr[6:8], port)
		a := addr.As16()
		var key [16]byte
		binary.BigEndian.PutUint32(key[0:4], magicCookie)
		copy(key[4:], txID)
		for i := 0; i < 16; i++ {
			attr[8+i] = a[i] ^ key[i]
		}
	}
	sw := []byte("gorget")
	swAttr := make([]byte, 4+len(sw)+(4-len(sw)%4)%4)
	binary.BigEndian.PutUint16(swAttr[0:2], attrSoftware)
	binary.BigEndian.PutUint16(swAttr[2:4], uint16(len(sw)))
	copy(swAttr[4:], sw)

	body := append(attr, swAttr...)
	resp := make([]byte, headerLen+len(body))
	binary.BigEndian.PutUint16(resp[0:2], bindingSuccess)
	binary.BigEndian.PutUint16(resp[2:4], uint16(len(body)))
	binary.BigEndian.PutUint32(resp[4:8], magicCookie)
	copy(resp[8:20], txID)
	copy(resp[20:], body)
	return resp, true
}

// ParseResponse extracts the XOR-MAPPED-ADDRESS from a Binding response (used by tests and clients).
func ParseResponse(b []byte) (netip.AddrPort, error) {
	if len(b) < headerLen || binary.BigEndian.Uint16(b[0:2]) != bindingSuccess {
		return netip.AddrPort{}, errors.New("not a binding success response")
	}
	txID := b[8:20]
	attrs := b[headerLen:]
	for len(attrs) >= 4 {
		typ := binary.BigEndian.Uint16(attrs[0:2])
		l := int(binary.BigEndian.Uint16(attrs[2:4]))
		if 4+l > len(attrs) {
			break
		}
		v := attrs[4 : 4+l]
		if typ == attrXorMapped && l >= 8 {
			port := binary.BigEndian.Uint16(v[2:4]) ^ uint16(magicCookie>>16)
			switch v[1] {
			case 0x01:
				x := binary.BigEndian.Uint32(v[4:8]) ^ magicCookie
				var a [4]byte
				binary.BigEndian.PutUint32(a[:], x)
				return netip.AddrPortFrom(netip.AddrFrom4(a), port), nil
			case 0x02:
				if l < 20 {
					break
				}
				var key [16]byte
				binary.BigEndian.PutUint32(key[0:4], magicCookie)
				copy(key[4:], txID)
				var a [16]byte
				for i := 0; i < 16; i++ {
					a[i] = v[4+i] ^ key[i]
				}
				return netip.AddrPortFrom(netip.AddrFrom16(a), port), nil
			}
		}
		attrs = attrs[4+l+(4-l%4)%4:]
	}
	return netip.AddrPort{}, errors.New("no XOR-MAPPED-ADDRESS")
}

// Request builds a Binding request with the given 12-byte transaction ID.
func Request(txID [12]byte) []byte {
	b := make([]byte, headerLen)
	binary.BigEndian.PutUint16(b[0:2], bindingRequest)
	binary.BigEndian.PutUint32(b[4:8], magicCookie)
	copy(b[8:], txID[:])
	return b
}
