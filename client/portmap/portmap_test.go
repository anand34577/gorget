package portmap

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

func TestNATPMPRequestLayout(t *testing.T) {
	b := natpmpMapRequest(41641, time.Hour)
	if len(b) != 12 || b[0] != 0 || b[1] != 1 {
		t.Fatalf("bad header: %v", b)
	}
	if binary.BigEndian.Uint16(b[4:]) != 41641 || binary.BigEndian.Uint32(b[8:]) != 3600 {
		t.Fatalf("port or lifetime wrong: %v", b)
	}
}

func TestPCPRequestLayout(t *testing.T) {
	var nonce [12]byte
	nonce[0] = 7
	b := pcpMapRequest(netip.MustParseAddr("192.168.1.20"), 41641, time.Hour, nonce)
	if len(b) != 60 || b[0] != 2 || b[1] != 1 || b[36] != 17 {
		t.Fatalf("bad request: %v", b)
	}
	if binary.BigEndian.Uint32(b[4:]) != 3600 || binary.BigEndian.Uint16(b[40:]) != 41641 || b[24] != 7 {
		t.Fatalf("fields wrong: %v", b)
	}
	// Our address travels as an IPv4-mapped IPv6 address.
	if b[18] != 0xff || b[19] != 0xff || b[20] != 192 || b[23] != 20 {
		t.Fatalf("client address wrong: %v", b[8:24])
	}
}

func TestFindControlURL(t *testing.T) {
	desc := []byte(`<root><device><deviceList><device><deviceList><device><serviceList>
	  <service><serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType><controlURL>/ctl/IPConn</controlURL></service>
	</serviceList></device></deviceList></device></deviceList></device></root>`)
	c, s := findControlURL(desc)
	if c != "/ctl/IPConn" || s != "urn:schemas-upnp-org:service:WANIPConnection:1" {
		t.Fatalf("got %q %q", c, s)
	}
	if c, _ := findControlURL([]byte("<root/>")); c != "" {
		t.Fatal("no service must give an empty control URL")
	}
}
