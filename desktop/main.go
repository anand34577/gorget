// Command gorget-desktop is the Gorget tray app and window. It controls the
// background service (gorget daemon) through its local API.
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/anand34577/gorget/client"
)

//go:embed all:frontend/dist
var distFS embed.FS

//go:embed icons/tray-on.png
var trayOn []byte

//go:embed icons/tray-off.png
var trayOff []byte

//go:embed icons/tray-template.png
var trayTemplate []byte

//go:embed icons/app.png
var appIcon []byte

func main() {
	hidden := false
	for _, a := range os.Args[1:] {
		if a == "--hidden" { // used by "start at login"
			hidden = true
		}
	}
	assets, err := fs.Sub(distFS, "frontend/dist")
	if err != nil {
		log.Fatal(err)
	}
	svc := newApp()
	app := application.New(application.Options{
		Name:        "Gorget",
		Description: "Gorget mesh VPN",
		Icon:        appIcon,
		Services:    []application.Service{application.NewService(svc)},
		Assets:      application.AssetOptions{Handler: application.BundledAssetFileServer(assets)},
		Mac:         application.MacOptions{ActivationPolicy: application.ActivationPolicyAccessory},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "net.gorget.desktop",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if w := mainWindow; w != nil {
					w.Show()
					w.Focus()
				}
			},
		},
	})
	svc.app = app

	mainWindow = app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Gorget",
		Width:            440,
		Height:           680,
		MinWidth:         380,
		MinHeight:        520,
		URL:              "/",
		Hidden:           hidden,
		BackgroundColour: application.NewRGB(21, 26, 33),
	})
	// Closing the window keeps the tray app running.
	mainWindow.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		mainWindow.Hide()
		e.Cancel()
	})

	setupTray(app, svc)

	// Push every state change to the window.
	svc.onChange(func(s Snapshot) { app.Event.Emit("snapshot", s) })

	ctx, cancel := context.WithCancel(context.Background())
	app.OnShutdown(cancel)
	// The UI thread only exists once the app runs; start following the service then.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go svc.watch(ctx)
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

var mainWindow *application.WebviewWindow

func showWindow() {
	mainWindow.Show()
	mainWindow.Focus()
}

// setupTray builds the tray icon and a menu that is rebuilt on every state change.
func setupTray(app *application.App, svc *App) {
	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayTemplate)
	} else {
		tray.SetIcon(trayOff)
	}
	tray.SetTooltip("Gorget")
	tray.OnClick(showWindow)

	// The daemon sends a fresh status every few seconds even when nothing visible
	// changed; only rebuild the menu (which also closes it if open) on real changes.
	var lastKey string
	var keyMu sync.Mutex
	svc.onChange(func(s Snapshot) {
		key := menuKey(s)
		keyMu.Lock()
		same := key == lastKey
		lastKey = key
		keyMu.Unlock()
		if same {
			return
		}
		// Menus must be touched on the UI thread.
		application.InvokeAsync(func() {
			menu := buildMenu(app, svc, s)
			tray.SetMenu(menu)
			if runtime.GOOS != "darwin" {
				if s.Daemon && s.Status != nil && s.Status.State == client.StateRunning {
					tray.SetIcon(trayOn)
				} else {
					tray.SetIcon(trayOff)
				}
			}
			tray.SetTooltip(headline(s))
		})
	})
	tray.SetMenu(buildMenu(app, svc, Snapshot{}))
}

// menuKey captures everything the tray menu and icon show.
func menuKey(s Snapshot) string {
	if !s.Daemon || s.Status == nil {
		return "nodaemon"
	}
	st := s.Status
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%v|%v|%s|", st.State, s.CanControl, st.CanChooseExit, st.ExitNodeID)
	if st.Self != nil {
		b.WriteString(st.Self.Name + "|" + st.Self.IPv4 + "|")
	}
	for _, p := range st.Peers {
		if p.ExitNode {
			fmt.Fprintf(&b, "%s:%s:%v,", p.ID, p.Name, p.Online)
		}
	}
	return b.String()
}

func headline(s Snapshot) string {
	if !s.Daemon || s.Status == nil {
		return "Gorget: service not running"
	}
	st := s.Status
	switch st.State {
	case client.StateRunning:
		if st.Self != nil {
			return "Gorget: connected as " + st.Self.Name
		}
		return "Gorget: connected"
	case client.StateConnecting:
		return "Gorget: connecting…"
	case client.StateStopped:
		return "Gorget: disconnected"
	case client.StateNeedsLogin, client.StateNoServer:
		return "Gorget: sign in"
	case client.StatePendingApproval:
		return "Gorget: waiting for approval"
	case client.StateBlocked:
		return "Gorget: blocked by security rules"
	}
	return "Gorget"
}

func buildMenu(app *application.App, svc *App, s Snapshot) *application.Menu {
	m := app.NewMenu()
	m.Add(headline(s)).SetEnabled(false)
	m.AddSeparator()

	if s.Daemon && s.Status != nil {
		st := s.Status
		canUse := s.CanControl
		switch st.State {
		case client.StateRunning, client.StateConnecting, client.StatePendingApproval:
			m.Add("Disconnect").SetEnabled(canUse).OnClick(func(*application.Context) { go svc.Down() })
		case client.StateStopped:
			m.Add("Connect").SetEnabled(canUse).OnClick(func(*application.Context) { go svc.Up() })
		default:
			m.Add("Sign in…").OnClick(func(*application.Context) { showWindow() })
		}
		if st.State == client.StateRunning && st.Self != nil {
			m.Add("This device: " + st.Self.IPv4).SetEnabled(false)
		}
		var exits []client.PeerView
		for _, p := range st.Peers {
			if p.ExitNode {
				exits = append(exits, p)
			}
		}
		if len(exits) > 0 && st.CanChooseExit && canUse {
			sub := m.AddSubmenu("Exit node")
			sub.AddCheckbox("None", st.ExitNodeID == "").OnClick(func(*application.Context) { go svc.SetExitNode("") })
			for _, p := range exits {
				p := p
				label := p.Name
				if !p.Online {
					label += " (offline)"
				}
				sub.AddCheckbox(label, st.ExitNodeID == p.ID).SetEnabled(p.Online).
					OnClick(func(*application.Context) { go svc.SetExitNode(p.ID) })
			}
		}
		m.AddSeparator()
	}
	m.Add("Open Gorget").OnClick(func(*application.Context) { showWindow() })
	m.Add("Quit").OnClick(func(*application.Context) { app.Quit() })
	return m
}

func init() {
	if runtime.GOOS == "linux" {
		// WebKitGTK's DMA-BUF renderer is flaky on some drivers; the window is simple.
		if os.Getenv("WEBKIT_DISABLE_DMABUF_RENDERER") == "" {
			_ = os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
		}
	}
}
