package portmap

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ---------- UPnP Internet Gateway Device ----------

var wanServices = []string{
	"urn:schemas-upnp-org:service:WANIPConnection:2",
	"urn:schemas-upnp-org:service:WANIPConnection:1",
	"urn:schemas-upnp-org:service:WANPPPConnection:1",
}

type upnpClient struct {
	control string // control URL
	service string // service type
	hc      *http.Client
}

// upnpMap finds the router (once) and maps the port.
func (m *Mapper) upnpMap(ctx context.Context, local netip.Addr, port uint16) (netip.AddrPort, error) {
	m.mu.Lock()
	up := m.upnp
	m.mu.Unlock()
	if up == nil {
		var err error
		if up, err = discoverUPnP(ctx); err != nil {
			return netip.AddrPort{}, err
		}
		m.mu.Lock()
		m.upnp = up
		m.mu.Unlock()
	}
	ext, err := up.addMapping(ctx, local, port)
	if err != nil {
		// The router may have changed: rediscover next time.
		m.mu.Lock()
		m.upnp = nil
		m.mu.Unlock()
	}
	return ext, err
}

// discoverUPnP finds an IGD with SSDP and reads its description.
func discoverUPnP(ctx context.Context) (*upnpClient, error) {
	pc, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, err
	}
	defer pc.Close()
	dst := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	var location string
	for _, st := range []string{"urn:schemas-upnp-org:device:InternetGatewayDevice:1", "urn:schemas-upnp-org:device:InternetGatewayDevice:2"} {
		msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: " + st + "\r\n\r\n"
		_, _ = pc.WriteTo([]byte(msg), dst)
	}
	_ = pc.SetReadDeadline(time.Now().Add(2500 * time.Millisecond))
	buf := make([]byte, 2048)
	for location == "" {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return nil, errors.New("no UPnP router answered")
		}
		for _, line := range strings.Split(string(buf[:n]), "\r\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "location") {
				if loc := strings.TrimSpace(v); locationOK(loc, from) {
					location = loc
				}
			}
		}
	}
	// The daemon runs privileged: never follow redirects or use a proxy for these
	// LAN requests, so a device answering SSDP can't point us anywhere else.
	hc := &http.Client{
		Timeout:       5 * time.Second,
		Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(location)
	if err != nil {
		return nil, err
	}
	control, service := findControlURL(body)
	if control == "" {
		return nil, errors.New("the router offers no port-mapping service")
	}
	cu, err := base.Parse(control)
	if err != nil {
		return nil, err
	}
	// The control URL must be on the router itself, never elsewhere.
	if cu.Hostname() != base.Hostname() {
		return nil, errors.New("ignoring a control URL on another host")
	}
	return &upnpClient{control: cu.String(), service: service, hc: hc}, nil
}

// locationOK accepts only a plain-HTTP description URL on the host that answered the
// discovery, at a private or link-local address (a router on the local network).
func locationOK(loc string, from net.Addr) bool {
	u, err := url.Parse(loc)
	if err != nil || u.Scheme != "http" || u.User != nil {
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !(ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return false
	}
	ua, ok := from.(*net.UDPAddr)
	if !ok {
		return false
	}
	src, ok := netip.AddrFromSlice(ua.IP)
	return ok && src.Unmap() == ip.Unmap()
}

type xmlDevice struct {
	Services []struct {
		Type    string `xml:"serviceType"`
		Control string `xml:"controlURL"`
	} `xml:"serviceList>service"`
	Devices []xmlDevice `xml:"deviceList>device"`
}

// findControlURL walks the device tree for a WAN connection service.
func findControlURL(desc []byte) (control, service string) {
	var root struct {
		Device xmlDevice `xml:"device"`
	}
	if xml.Unmarshal(desc, &root) != nil {
		return "", ""
	}
	var walk func(d xmlDevice) bool
	walk = func(d xmlDevice) bool {
		for _, want := range wanServices {
			for _, s := range d.Services {
				if s.Type == want {
					control, service = s.Control, s.Type
					return true
				}
			}
		}
		for _, c := range d.Devices {
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(root.Device)
	return control, service
}

func (u *upnpClient) soap(ctx context.Context, action string, args [][2]string) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:%s xmlns:u="%s">`, action, u.service)
	for _, a := range args {
		fmt.Fprintf(&b, "<%s>", a[0])
		_ = xml.EscapeText(&b, []byte(a[1]))
		fmt.Fprintf(&b, "</%s>", a[0])
	}
	fmt.Fprintf(&b, "</u:%s></s:Body></s:Envelope>", action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.control, &b)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+u.service+"#"+action+`"`)
	resp, err := u.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("UPnP %s failed (HTTP %d)", action, resp.StatusCode)
	}
	return out, nil
}

func (u *upnpClient) addMapping(ctx context.Context, local netip.Addr, port uint16) (netip.AddrPort, error) {
	p := strconv.Itoa(int(port))
	_, err := u.soap(ctx, "AddPortMapping", [][2]string{
		{"NewRemoteHost", ""}, {"NewExternalPort", p}, {"NewProtocol", "UDP"}, {"NewInternalPort", p},
		{"NewInternalClient", local.String()}, {"NewEnabled", "1"}, {"NewPortMappingDescription", "Gorget"},
		{"NewLeaseDuration", strconv.Itoa(int(Lease / time.Second))},
	})
	if err != nil {
		return netip.AddrPort{}, err
	}
	out, err := u.soap(ctx, "GetExternalIPAddress", nil)
	if err != nil {
		return netip.AddrPort{}, err
	}
	var env struct {
		IP string `xml:"Body>GetExternalIPAddressResponse>NewExternalIPAddress"`
	}
	if xml.Unmarshal(out, &env) != nil {
		return netip.AddrPort{}, errors.New("unreadable answer from the router")
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(env.IP))
	if err != nil || ip.IsUnspecified() || ip.IsPrivate() {
		return netip.AddrPort{}, errors.New("the router has no public address")
	}
	return netip.AddrPortFrom(ip, port), nil
}

func (u *upnpClient) deleteMapping(ctx context.Context, port uint16) error {
	_, err := u.soap(ctx, "DeletePortMapping", [][2]string{
		{"NewRemoteHost", ""}, {"NewExternalPort", strconv.Itoa(int(port))}, {"NewProtocol", "UDP"},
	})
	return err
}
