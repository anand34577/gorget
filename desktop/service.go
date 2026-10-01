package main

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/anand34577/gorget/client"
	"github.com/anand34577/gorget/client/filetransfer"
	"github.com/anand34577/gorget/client/localapi"
)

// Snapshot is what the window and tray render: the client status plus whether the
// background service is reachable and whether this user may change things.
type Snapshot struct {
	Daemon     bool           `json:"daemon"`
	CanControl bool           `json:"can_control"`
	Error      string         `json:"error,omitempty"`
	Status     *client.Status `json:"status,omitempty"`
}

// App is bound to the frontend (method names are called as main.App.<Name>).
// Everything goes through the daemon's local API, so the window needs no privileges.
type App struct {
	api *localapi.Client
	app *application.App

	mu   sync.Mutex
	snap Snapshot
	subs []func(Snapshot)
}

func newApp() *App { return &App{api: localapi.NewClient("")} }

func (a *App) onChange(f func(Snapshot)) {
	a.mu.Lock()
	a.subs = append(a.subs, f)
	a.mu.Unlock()
}

func (a *App) set(s Snapshot) {
	a.mu.Lock()
	a.snap = s
	subs := append([]func(Snapshot){}, a.subs...)
	a.mu.Unlock()
	for _, f := range subs {
		f(s)
	}
}

// Snapshot returns the latest known state (the window calls this once on load).
func (a *App) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snap
}

// watch follows the daemon forever, reconnecting when the service restarts.
func (a *App) watch(ctx context.Context) {
	for ctx.Err() == nil {
		who, err := a.api.WhoAmI(ctx)
		if err != nil {
			a.set(Snapshot{Daemon: false, Error: friendlyDaemonError(err)})
			sleep(ctx, 2*time.Second)
			continue
		}
		err = a.api.Watch(ctx, func(st client.Status) {
			a.set(Snapshot{Daemon: true, CanControl: who.CanControl, Status: &st})
		})
		if ctx.Err() != nil {
			return
		}
		a.set(Snapshot{Daemon: false, Error: friendlyDaemonError(err)})
		sleep(ctx, 2*time.Second)
	}
}

func friendlyDaemonError(err error) string {
	switch {
	case err == nil, errors.Is(err, localapi.ErrNoDaemon):
		return "The Gorget service isn't running."
	case errors.Is(err, localapi.ErrUntrustedDaemon):
		return "Another program is pretending to be the Gorget service, so this app won't talk to it. Reinstall Gorget or ask your administrator."
	}
	return err.Error()
}

// Platform tells the window which operating system it runs on (for instructions).
func (a *App) Platform() string { return runtime.GOOS }

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (a *App) ctx() context.Context { return context.Background() }

// ---- actions called from the window and the tray ----

func (a *App) SetServer(url string) error { return a.api.SetServer(a.ctx(), url) }

// Login starts a browser sign-in, opens the browser and returns the code to show.
func (a *App) Login() (localapi.LoginInfo, error) {
	info, err := a.api.Login(a.ctx())
	if err != nil {
		return info, err
	}
	a.OpenURL(info.URL)
	return info, nil
}

func (a *App) LoginSetupKey(key string) error { return a.api.LoginSetupKey(a.ctx(), key) }
func (a *App) Up() error                      { return a.api.Up(a.ctx()) }
func (a *App) Down() error                    { return a.api.Down(a.ctx()) }
func (a *App) Logout() error                  { return a.api.Logout(a.ctx()) }

// SetPrefs applies a partial preferences object, e.g. {"kill_switch": true}.
func (a *App) SetPrefs(patch map[string]any) error {
	_, err := a.api.SetPrefs(a.ctx(), patch)
	return err
}

// SetExitNode picks an exit node by device id ("" turns it off).
func (a *App) SetExitNode(id string) error {
	return a.SetPrefs(map[string]any{"exit_node_id": id})
}

// OpenURL opens http(s) links in the system browser.
func (a *App) OpenURL(u string) {
	if a.app == nil || !(strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")) {
		return
	}
	_ = a.app.Browser.OpenURL(u)
}

// ---- files received from the user's other devices ----

// Files lists the received files (newest first).
func (a *App) Files() ([]filetransfer.Incoming, error) { return a.api.Files(a.ctx()) }

// SaveFile asks where to save a received file, copies it there and removes it from the inbox.
// It returns the chosen path ("" when the user cancelled).
func (a *App) SaveFile(name string) (string, error) {
	if a.app == nil {
		return "", errors.New("not ready")
	}
	dst, err := a.app.Dialog.SaveFile().SetFilename(name).PromptForSingleSelection()
	if err != nil || dst == "" {
		return "", nil // cancelled
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	err = a.api.DownloadFile(a.ctx(), name, out)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
		return "", err
	}
	_ = a.api.DeleteFile(a.ctx(), name)
	return dst, nil
}

// DeleteFile discards a received file.
func (a *App) DeleteFile(name string) error { return a.api.DeleteFile(a.ctx(), name) }

// SendFile asks which file to send and sends it to the given device (by id).
// It returns the file name that was sent ("" when the user cancelled).
func (a *App) SendFile(peerID string) (string, error) {
	if a.app == nil {
		return "", errors.New("not ready")
	}
	snap := a.Snapshot()
	if snap.Status == nil || snap.Status.State != client.StateRunning {
		return "", errors.New("connect first")
	}
	var addr netip.Addr
	for _, p := range snap.Status.Peers {
		if p.ID == peerID {
			for _, s := range []string{p.IPv4, p.IPv6} {
				if x, err := netip.ParseAddr(s); err == nil {
					addr = x
					break
				}
			}
		}
	}
	if !addr.IsValid() {
		return "", errors.New("that device has no address")
	}
	path, err := a.app.Dialog.OpenFile().CanChooseFiles(true).SetTitle("Choose a file to send").PromptForSingleSelection()
	if err != nil || path == "" {
		return "", nil // cancelled
	}
	ctx, cancel := context.WithTimeout(a.ctx(), 6*time.Hour)
	defer cancel()
	name := filepath.Base(path)
	// Report progress to the window at most a few times a second.
	var last time.Time
	progress := func(sent, total int64) {
		if now := time.Now(); now.Sub(last) > 250*time.Millisecond || sent == total {
			last = now
			a.app.Event.Emit("send-progress", SendProgress{Name: name, Sent: sent, Total: total})
		}
	}
	err = filetransfer.Send(ctx, addr, path, progress)
	a.app.Event.Emit("send-progress", SendProgress{Name: name, Done: true})
	if err != nil {
		return "", err
	}
	return name, nil
}

// SendProgress is emitted while a file is being sent.
type SendProgress struct {
	Name  string `json:"name"`
	Sent  int64  `json:"sent"`
	Total int64  `json:"total"`
	Done  bool   `json:"done"`
}
