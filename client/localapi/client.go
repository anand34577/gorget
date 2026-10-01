package localapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/anand34577/gorget/client"
	"github.com/anand34577/gorget/client/filetransfer"
)

// ErrNoDaemon is returned when nothing is listening on the local API address.
var ErrNoDaemon = errors.New(noDaemonHint())

// ErrUntrustedDaemon is returned when the address is served by a process that isn't
// the privileged Gorget service.
var ErrUntrustedDaemon = errors.New("the local Gorget address is served by a program that isn't the Gorget service; not sending anything to it")

func noDaemonHint() string {
	if runtime.GOOS == "windows" {
		return "the Gorget service isn't running. Start it from an Administrator terminal with: gorget install-service (or reinstall Gorget)"
	}
	return "the Gorget service isn't running. Start it with: sudo gorget install-service (or run sudo gorget daemon in another terminal)"
}

// requestTimeout bounds one call so the CLI never hangs on a stuck daemon. Signing
// in with a setup key contacts the server, so it gets generous time.
const requestTimeout = 2 * time.Minute

// Client talks to the daemon.
type Client struct {
	hc *http.Client
}

// NewClient connects to the daemon at the default address (or addr when set).
func NewClient(addr string) *Client {
	if addr == "" {
		addr = DefaultAddr()
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			c, err := dial(ctx, addr)
			if err != nil {
				return nil, fmt.Errorf("%w (%v)", ErrNoDaemon, err)
			}
			return c, nil
		},
		DisableKeepAlives: false,
	}
	return &Client{hc: &http.Client{Transport: tr}}
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, requestTimeout)
		defer cancel()
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://gorget"+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return unwrapDial(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e apiError
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func unwrapDial(err error) error {
	switch {
	case errors.Is(err, ErrUntrustedDaemon):
		return ErrUntrustedDaemon
	case errors.Is(err, ErrNoDaemon):
		return ErrNoDaemon
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("the Gorget service didn't answer in time; check its log (gorget.log in the data folder)")
	}
	return err
}

func (c *Client) Status(ctx context.Context) (client.Status, error) {
	var s client.Status
	return s, c.do(ctx, "GET", "/v1/status", nil, &s)
}

func (c *Client) WhoAmI(ctx context.Context) (WhoAmI, error) {
	var w WhoAmI
	return w, c.do(ctx, "GET", "/v1/whoami", nil, &w)
}

func (c *Client) Prefs(ctx context.Context) (client.Prefs, error) {
	var p client.Prefs
	return p, c.do(ctx, "GET", "/v1/prefs", nil, &p)
}

// SetPrefs applies the given fields (a map or struct with the JSON names of client.Prefs).
func (c *Client) SetPrefs(ctx context.Context, patch any) (client.Prefs, error) {
	var p client.Prefs
	return p, c.do(ctx, "PATCH", "/v1/prefs", patch, &p)
}

func (c *Client) SetServer(ctx context.Context, url string) error {
	return c.do(ctx, "POST", "/v1/server", map[string]string{"url": url}, nil)
}

func (c *Client) Login(ctx context.Context) (LoginInfo, error) {
	var l LoginInfo
	return l, c.do(ctx, "POST", "/v1/login", nil, &l)
}

func (c *Client) LoginSetupKey(ctx context.Context, key string) error {
	return c.do(ctx, "POST", "/v1/login/setup-key", map[string]string{"key": key}, nil)
}

func (c *Client) Up(ctx context.Context) error     { return c.do(ctx, "POST", "/v1/up", nil, nil) }
func (c *Client) Down(ctx context.Context) error   { return c.do(ctx, "POST", "/v1/down", nil, nil) }
func (c *Client) Logout(ctx context.Context) error { return c.do(ctx, "POST", "/v1/logout", nil, nil) }
func (c *Client) Quit(ctx context.Context) error   { return c.do(ctx, "POST", "/v1/quit", nil, nil) }

// Watch calls f for every status event until ctx ends or the stream breaks.
func (c *Client) Watch(ctx context.Context, f func(client.Status)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://gorget/v1/events", nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return unwrapDial(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		var st client.Status
		if json.Unmarshal([]byte(line), &st) == nil {
			f(st)
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}

// WaitForDaemon polls until the daemon answers or the timeout passes.
func (c *Client) WaitForDaemon(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := c.Status(ctx); err == nil {
			return nil
		} else if time.Now().After(deadline) || ctx.Err() != nil {
			return err
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// Files lists the files received from the user's other devices.
func (c *Client) Files(ctx context.Context) ([]filetransfer.Incoming, error) {
	var out []filetransfer.Incoming
	return out, c.do(ctx, "GET", "/v1/files", nil, &out)
}

// DownloadFile copies a received file to w.
func (c *Client) DownloadFile(ctx context.Context, name string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://gorget/v1/files/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return unwrapDial(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var e apiError
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return errors.New(orStatus(e.Error, resp.Status))
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func orStatus(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// DeleteFile removes a received file.
func (c *Client) DeleteFile(ctx context.Context, name string) error {
	return c.do(ctx, "DELETE", "/v1/files/"+url.PathEscape(name), nil, nil)
}

// BypassApp keeps a process outside the tunnel (Linux).
func (c *Client) BypassApp(ctx context.Context, pid int) error {
	return c.do(ctx, "POST", "/v1/app-bypass", map[string]int{"pid": pid}, nil)
}

// skipServerCheck turns off the check that the daemon runs privileged (tests only).
var skipServerCheck = false
