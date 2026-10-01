// Package filetransfer sends files directly between two of a person's devices over
// the Gorget network. The receiving device listens on its overlay address only
// (never on the physical network) and accepts files only from devices signed in as
// the same user; the file goes straight through the encrypted tunnel and never
// touches the server.
package filetransfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Port is the TCP port of the receiver on a device's overlay address.
const Port = 7765

// DefaultMaxBytes limits one incoming file.
const DefaultMaxBytes = 32 << 30

// Identity answers who is on the other end of a connection.
type Identity interface {
	// SenderUser returns the user signed in on the peer that owns the overlay address.
	SenderUser(ip netip.Addr) (peerName, user string, ok bool)
	// SelfUser is the user signed in on this device.
	SelfUser() string
}

// Incoming describes a received file.
type Incoming struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	From     string    `json:"from"`
	Received time.Time `json:"received"`
}

// Receiver stores incoming files in a directory and serves the upload endpoint.
type Receiver struct {
	dir      string
	id       Identity
	log      *slog.Logger
	maxBytes int64

	mu    sync.Mutex
	addrs []netip.Addr
	srvs  []*http.Server

	nameMu sync.Mutex // serialises picking a free name and moving the file there
}

// NewReceiver creates a receiver storing files in dir.
func NewReceiver(dir string, id Identity, log *slog.Logger) *Receiver {
	if log == nil {
		log = slog.Default()
	}
	return &Receiver{dir: dir, id: id, log: log, maxBytes: DefaultMaxBytes}
}

// Sync binds the receiver to exactly the given overlay addresses (none = stop listening).
func (r *Receiver) Sync(addrs []netip.Addr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sameAddrs(addrs, r.addrs) {
		return
	}
	for _, s := range r.srvs {
		_ = s.Close()
	}
	r.srvs, r.addrs = nil, nil
	if len(addrs) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Join(r.dir, ".meta"), 0o700); err != nil {
		r.log.Warn("file inbox unavailable", "err", err)
		return
	}
	for _, a := range addrs {
		ln, err := net.Listen("tcp", netip.AddrPortFrom(a, Port).String())
		if err != nil {
			r.log.Debug("file receiver not started", "addr", a, "err", err)
			continue
		}
		srv := &http.Server{Handler: http.HandlerFunc(r.serve), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute}
		go func() { _ = srv.Serve(ln) }()
		r.srvs = append(r.srvs, srv)
	}
	r.addrs = append([]netip.Addr(nil), addrs...)
}

func sameAddrs(a, b []netip.Addr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Close stops listening.
func (r *Receiver) Close() { r.Sync(nil) }

// safeName keeps only a plain file name.
func safeName(raw string) string {
	name := filepath.Base(strings.ReplaceAll(raw, `\`, "/"))
	name = strings.Map(func(c rune) rune {
		if c < 32 || c == 127 || strings.ContainsRune(`<>:"|?*`, c) {
			return '_'
		}
		return c
	}, name)
	name = strings.Trim(name, ". ")
	if len(name) > 200 {
		ext := filepath.Ext(name)
		if len(ext) > 20 {
			ext = ""
		}
		name = name[:200-len(ext)] + ext
	}
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	// Windows treats CON, NUL, COM1 and friends as devices, with or without an extension.
	stem, _, _ := strings.Cut(name, ".")
	switch strings.ToUpper(strings.TrimSpace(stem)) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return "_" + name
	}
	return name
}

// uniqueName returns a name not yet used in dir ("a.txt" -> "a (1).txt").
func uniqueName(dir, name string) string {
	if _, err := os.Lstat(filepath.Join(dir, name)); errors.Is(err, os.ErrNotExist) {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		cand := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Lstat(filepath.Join(dir, cand)); errors.Is(err, os.ErrNotExist) {
			return cand
		}
	}
	return name + "-" + randomHex(4)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (r *Receiver) serve(w http.ResponseWriter, req *http.Request) {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	ip, perr := netip.ParseAddr(host)
	if err != nil || perr != nil {
		http.Error(w, "bad address", http.StatusBadRequest)
		return
	}
	peer, user, ok := r.id.SenderUser(ip)
	self := r.id.SelfUser()
	if !ok || user == "" || self == "" || !strings.EqualFold(user, self) {
		http.Error(w, "files are only accepted from your own devices", http.StatusForbidden)
		return
	}
	const prefix = "/v1/put/"
	if req.Method != http.MethodPut || !strings.HasPrefix(req.URL.EscapedPath(), prefix) {
		http.Error(w, "use PUT "+prefix+"<name>", http.StatusMethodNotAllowed)
		return
	}
	raw, err := url.PathUnescape(strings.TrimPrefix(req.URL.EscapedPath(), prefix))
	if err != nil {
		http.Error(w, "bad name", http.StatusBadRequest)
		return
	}
	if req.ContentLength > r.maxBytes {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	if err := os.MkdirAll(filepath.Join(r.dir, ".meta"), 0o700); err != nil {
		http.Error(w, "inbox unavailable", http.StatusInternalServerError)
		return
	}
	name := uniqueName(r.dir, safeName(raw))
	tmp := filepath.Join(r.dir, ".meta", randomHex(8)+".part")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		http.Error(w, "inbox unavailable", http.StatusInternalServerError)
		return
	}
	n, err := io.Copy(f, http.MaxBytesReader(w, req.Body, r.maxBytes))
	cerr := f.Close()
	if err != nil || cerr != nil {
		_ = os.Remove(tmp)
		http.Error(w, "upload failed", http.StatusBadRequest)
		return
	}
	if req.ContentLength >= 0 && n != req.ContentLength {
		_ = os.Remove(tmp)
		http.Error(w, "incomplete upload", http.StatusBadRequest)
		return
	}
	// Pick the final name and move under a lock, so two uploads of "a.txt" at the
	// same moment can't both choose the same free name (rename would overwrite).
	r.nameMu.Lock()
	name = uniqueName(r.dir, name)
	err = os.Rename(tmp, filepath.Join(r.dir, name))
	r.nameMu.Unlock()
	if err != nil {
		_ = os.Remove(tmp)
		http.Error(w, "could not store the file", http.StatusInternalServerError)
		return
	}
	meta, _ := json.Marshal(Incoming{Name: name, Size: n, From: peer, Received: time.Now().UTC()})
	_ = os.WriteFile(filepath.Join(r.dir, ".meta", name+".json"), meta, 0o600)
	r.log.Info("file received", "name", name, "bytes", n, "from", peer)
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(name))
}

// List returns received files, newest first.
func (r *Receiver) List() []Incoming {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return []Incoming{}
	}
	out := []Incoming{}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		in := Incoming{Name: e.Name(), Size: info.Size(), Received: info.ModTime()}
		if b, err := os.ReadFile(filepath.Join(r.dir, ".meta", e.Name()+".json")); err == nil {
			var m Incoming
			if json.Unmarshal(b, &m) == nil {
				in.From, in.Received = m.From, m.Received
			}
		}
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Received.After(out[j].Received) })
	return out
}

// Open opens a received file for reading.
func (r *Receiver) Open(name string) (*os.File, error) {
	if name != safeName(name) {
		return nil, os.ErrNotExist
	}
	return os.Open(filepath.Join(r.dir, name))
}

// Delete removes a received file.
func (r *Receiver) Delete(name string) error {
	if name != safeName(name) {
		return os.ErrNotExist
	}
	if err := os.Remove(filepath.Join(r.dir, name)); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(r.dir, ".meta", name+".json"))
	return nil
}

// Send uploads a local file to the receiver on the device with the given overlay address.
// progress (optional) is called with the bytes sent so far.
func Send(ctx context.Context, to netip.Addr, path string, progress func(sent, total int64)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return errors.New("send a file, not a folder (zip it first)")
	}
	body := io.Reader(f)
	if progress != nil {
		body = &progressReader{r: f, total: st.Size(), f: progress}
	}
	u := "http://" + netip.AddrPortFrom(to, Port).String() + "/v1/put/" + url.PathEscape(filepath.Base(path))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, body)
	if err != nil {
		return err
	}
	req.ContentLength = st.Size()
	if st.Size() == 0 {
		req.Body = http.NoBody
	}
	hc := &http.Client{Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext}}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach that device (is it online, running Gorget, and allowed by your access rules?): %w", err)
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode != http.StatusCreated {
		return errors.New(strings.TrimSpace(string(msg)) + " (HTTP " + strconv.Itoa(resp.StatusCode) + ")")
	}
	return nil
}

type progressReader struct {
	r     io.Reader
	total int64
	sent  int64
	f     func(sent, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.sent += int64(n)
	p.f(p.sent, p.total)
	return n, err
}
