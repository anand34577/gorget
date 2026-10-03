package core

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/anand34577/gorget/internal/store"
)

const keyNotify = "notifications"

// GotifySettings sends notifications to a Gotify server (https://gotify.net).
type GotifySettings struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	// TokenSealed is the application token encrypted with the master key. It never leaves the
	// server; TokenSet tells the console one exists.
	TokenSealed string `json:"token_sealed,omitempty"`
	TokenSet    bool   `json:"token_set"`
	// Priority is Gotify's 0-10 scale. Important events (an unrecognised sign-in, a blocked
	// device) are raised to at least 8.
	Priority   int  `json:"priority"`
	SkipVerify bool `json:"skip_verify"`
}

// NtfySettings sends notifications through ntfy (https://ntfy.sh or your own server).
type NtfySettings struct {
	Enabled     bool   `json:"enabled"`
	URL         string `json:"url"`
	Topic       string `json:"topic"`
	TokenSealed string `json:"token_sealed,omitempty"`
	TokenSet    bool   `json:"token_set"`
	// Priority is ntfy's 1-5 scale; important events are sent as 5.
	Priority   int  `json:"priority"`
	SkipVerify bool `json:"skip_verify"`
}

// LoginAlerts controls the "someone signed in" notification.
type LoginAlerts struct {
	// Mode: off, new (a browser or country this account hasn't used before) or all.
	Mode string `json:"mode"`
	// ToUser emails the person whose account was used.
	ToUser bool `json:"to_user"`
	// ToAdmins emails the notification recipients too.
	ToAdmins bool `json:"to_admins"`
	// ToPush sends it to Gotify / ntfy.
	ToPush bool `json:"to_push"`
}

// NotificationSettings holds the push channels and the sign-in alerts. Email has its own
// settings (SMTP account and the events to mail).
type NotificationSettings struct {
	Gotify GotifySettings `json:"gotify"`
	Ntfy   NtfySettings   `json:"ntfy"`
	// Events chooses which events are pushed (same list as the email events).
	Events EmailNotify `json:"events"`
	Login  LoginAlerts `json:"login"`
}

func defaultNotifications() NotificationSettings {
	return NotificationSettings{
		Gotify: GotifySettings{Priority: 5},
		Ntfy:   NtfySettings{URL: "https://ntfy.sh", Priority: 3},
		Events: EmailNotify{
			DevicePending: true, NewCountry: true, DeviceBlocked: true, KeyExpiring: true,
			AccessRequests: true, RouteAdvertised: true, LoginLockout: true,
		},
		Login: LoginAlerts{Mode: "new", ToUser: true, ToPush: true},
	}
}

func (n NotificationSettings) redacted() NotificationSettings {
	n.Gotify.TokenSet, n.Gotify.TokenSealed = n.Gotify.TokenSealed != "", ""
	n.Ntfy.TokenSet, n.Ntfy.TokenSealed = n.Ntfy.TokenSealed != "", ""
	return n
}

// on reports whether the toggle for an event kind is set.
func (n EmailNotify) on(kind string) bool {
	switch kind {
	case "pending":
		return n.DevicePending
	case "added":
		return n.DeviceAdded
	case "country":
		return n.NewCountry
	case "blocked":
		return n.DeviceBlocked
	case "expiring":
		return n.KeyExpiring
	case "access":
		return n.AccessRequests
	case "route":
		return n.RouteAdvertised
	case "lockout":
		return n.LoginLockout
	case "offline":
		return n.DeviceOffline
	case "online":
		return n.DeviceOnline
	}
	return false
}

// NotificationUpdate is a change to the notification settings. A nil token keeps the stored
// one; an empty string removes it.
type NotificationUpdate struct {
	Settings    NotificationSettings
	GotifyToken *string
	NtfyToken   *string
}

var ntfyTopic = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// SaveNotificationSettings validates and stores the push channels and sign-in alerts.
func (c *Core) SaveNotificationSettings(ctx context.Context, in NotificationUpdate) (AllSettings, error) {
	return c.SaveSettings(ctx, keyNotify, func(s *AllSettings) error {
		n := in.Settings
		n.Gotify.TokenSealed, n.Ntfy.TokenSealed = s.Notifications.Gotify.TokenSealed, s.Notifications.Ntfy.TokenSealed
		seal := func(dst *string, tok *string) error {
			if tok == nil {
				return nil
			}
			if *tok == "" {
				*dst = ""
				return nil
			}
			v, err := c.Box.Seal(strings.TrimSpace(*tok))
			if err != nil {
				return err
			}
			*dst = v
			return nil
		}
		if err := seal(&n.Gotify.TokenSealed, in.GotifyToken); err != nil {
			return err
		}
		if err := seal(&n.Ntfy.TokenSealed, in.NtfyToken); err != nil {
			return err
		}
		n.Gotify.URL = strings.TrimRight(strings.TrimSpace(n.Gotify.URL), "/")
		n.Ntfy.URL = strings.TrimRight(strings.TrimSpace(n.Ntfy.URL), "/")
		n.Ntfy.Topic = strings.TrimSpace(n.Ntfy.Topic)
		n.Gotify.TokenSet, n.Ntfy.TokenSet = false, false
		s.Notifications = n
		return nil
	})
}

func validateNotifications(n NotificationSettings) error {
	httpURL := func(what, raw string) error {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return invalid("%s address must look like https://host", what)
		}
		return nil
	}
	if n.Gotify.Enabled {
		if err := httpURL("Gotify", n.Gotify.URL); err != nil {
			return err
		}
		if n.Gotify.TokenSealed == "" {
			return invalid("Gotify needs an application token (create an application in Gotify and paste its token)")
		}
	}
	if n.Gotify.Priority < 0 || n.Gotify.Priority > 10 {
		return invalid("Gotify priority must be between 0 and 10")
	}
	if n.Ntfy.Enabled {
		if err := httpURL("ntfy", n.Ntfy.URL); err != nil {
			return err
		}
		if !ntfyTopic.MatchString(n.Ntfy.Topic) {
			return invalid("the ntfy topic may contain letters, digits, - and _ (up to 64 characters)")
		}
	}
	if n.Ntfy.Priority < 1 || n.Ntfy.Priority > 5 {
		return invalid("ntfy priority must be between 1 and 5")
	}
	switch n.Login.Mode {
	case "off", "new", "all":
	default:
		return invalid("sign-in alerts must be off, new or all")
	}
	return nil
}

// ---------- delivery ----------

type pushMsg struct {
	Title  string
	Body   string // plain text, one fact per line
	Link   string
	Urgent bool
	Tags   []string // ntfy emoji tags
	Facts  [][2]string
	Intro  string
}

type pushStatus struct {
	mu        sync.Mutex
	Sent      int
	Failed    int
	LastSent  int64
	LastErr   string
	LastErrAt int64
}

func (p *pushStatus) record(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.LastErr, p.LastErrAt = err.Error(), store.Now()
		p.Failed++
		return
	}
	p.LastSent = store.Now()
	p.Sent++
}

// PushStatus reports delivery results since the server started.
func (c *Core) PushStatus() map[string]any {
	p := &c.push
	p.mu.Lock()
	defer p.mu.Unlock()
	return map[string]any{"sent": p.Sent, "failed": p.Failed, "last_sent": p.LastSent, "last_error": p.LastErr, "last_error_at": p.LastErrAt, "queued": len(c.pushq)}
}

// PushEnabled reports whether at least one push channel is switched on and complete.
func (c *Core) PushEnabled() bool {
	n := c.Settings().Notifications
	return (n.Gotify.Enabled && n.Gotify.URL != "" && n.Gotify.TokenSealed != "") || (n.Ntfy.Enabled && n.Ntfy.URL != "" && n.Ntfy.Topic != "")
}

func (c *Core) queuePush(m pushMsg) {
	if !c.PushEnabled() {
		return
	}
	select {
	case c.pushq <- m:
	default:
		c.Log.Warn("push queue full; message dropped", "title", m.Title)
	}
}

func (c *Core) runPush(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-c.pushq:
			var err error
			for attempt := 0; attempt < 3; attempt++ {
				sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
				err = c.deliverPush(sctx, m, "")
				cancel()
				if err == nil || ctx.Err() != nil {
					break
				}
				select {
				case <-ctx.Done():
				case <-time.After(time.Duration(attempt+1) * 10 * time.Second):
				}
			}
			c.push.record(err)
			if err != nil {
				c.Log.Warn("push notification not sent", "title", m.Title, "err", err)
			}
		}
	}
}

// deliverPush sends to every enabled channel (or only to channel, "gotify" or "ntfy", when set).
func (c *Core) deliverPush(ctx context.Context, m pushMsg, channel string) error {
	n := c.Settings().Notifications
	var errs []error
	if (channel == "" || channel == "gotify") && (n.Gotify.Enabled || channel == "gotify") && n.Gotify.URL != "" {
		if err := c.sendGotify(ctx, n.Gotify, m); err != nil {
			errs = append(errs, fmt.Errorf("Gotify: %w", err))
		}
	}
	if (channel == "" || channel == "ntfy") && (n.Ntfy.Enabled || channel == "ntfy") && n.Ntfy.URL != "" {
		if err := c.sendNtfy(ctx, n.Ntfy, m); err != nil {
			errs = append(errs, fmt.Errorf("ntfy: %w", err))
		}
	}
	return errors.Join(errs...)
}

// SendTestPush sends a test message through one channel and returns its error.
func (c *Core) SendTestPush(ctx context.Context, channel string) error {
	n := c.Settings().Notifications
	switch channel {
	case "gotify":
		if n.Gotify.URL == "" || n.Gotify.TokenSealed == "" {
			return invalid("save the Gotify address and token first")
		}
	case "ntfy":
		if n.Ntfy.URL == "" || n.Ntfy.Topic == "" {
			return invalid("save the ntfy address and topic first")
		}
	default:
		return invalid("unknown channel")
	}
	netName := c.Settings().Network.Name
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := c.deliverPush(ctx, pushMsg{
		Title: "Test message from " + netName,
		Intro: "This is a test from your Gorget server. If you can read it, notifications work.",
		Link:  strings.TrimSuffix(c.Cfg.PublicURL, "/"),
		Tags:  []string{"white_check_mark"},
	}, channel)
	c.push.record(err)
	return err
}

func pushClient(skipVerify bool) *http.Client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 10 * time.Second}
	if skipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in for a private server with its own certificate
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: tr}
}

func (m pushMsg) markdown() string {
	var b strings.Builder
	if m.Intro != "" {
		b.WriteString(m.Intro + "\n\n")
	}
	for _, f := range m.Facts {
		if f[1] != "" {
			fmt.Fprintf(&b, "- **%s:** %s\n", f[0], f[1])
		}
	}
	if m.Link != "" {
		fmt.Fprintf(&b, "\n[Open in Gorget](%s)\n", m.Link)
	}
	return strings.TrimSpace(b.String())
}

func (m pushMsg) text() string {
	var b strings.Builder
	if m.Intro != "" {
		b.WriteString(m.Intro + "\n")
	}
	for _, f := range m.Facts {
		if f[1] != "" {
			fmt.Fprintf(&b, "%s: %s\n", f[0], f[1])
		}
	}
	return strings.TrimSpace(b.String())
}

func doJSON(ctx context.Context, hc *http.Client, endpoint string, headers map[string]string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Gorget/"+Version)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.TrimSpace(string(b))
		switch resp.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("the server refused the token (HTTP %d); check it and the application/topic permissions", resp.StatusCode)
		case http.StatusNotFound:
			return fmt.Errorf("HTTP 404: check the address (does it end in the right path?) %s", msg)
		}
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, msg)
	}
	return nil
}

func (c *Core) sendGotify(ctx context.Context, g GotifySettings, m pushMsg) error {
	token, err := c.Box.Open(g.TokenSealed)
	if err != nil || token == "" {
		return errors.New("the stored token can't be decrypted; enter it again")
	}
	prio := g.Priority
	if m.Urgent && prio < 8 {
		prio = 8
	}
	extras := map[string]any{"client::display": map[string]string{"contentType": "text/markdown"}}
	if m.Link != "" {
		extras["client::notification"] = map[string]any{"click": map[string]string{"url": m.Link}}
	}
	payload := map[string]any{"title": m.Title, "message": m.markdown(), "priority": prio, "extras": extras}
	return doJSON(ctx, pushClient(g.SkipVerify), g.URL+"/message", map[string]string{"X-Gotify-Key": token}, payload)
}

func (c *Core) sendNtfy(ctx context.Context, n NtfySettings, m pushMsg) error {
	prio := n.Priority
	if m.Urgent {
		prio = 5
	}
	payload := map[string]any{"topic": n.Topic, "title": m.Title, "message": m.text(), "priority": prio, "markdown": false}
	if len(m.Tags) > 0 {
		payload["tags"] = m.Tags
	}
	if m.Link != "" {
		payload["click"] = m.Link
	}
	hdr := map[string]string{}
	if n.TokenSealed != "" {
		tok, err := c.Box.Open(n.TokenSealed)
		if err != nil {
			return errors.New("the stored token can't be decrypted; enter it again")
		}
		hdr["Authorization"] = "Bearer " + tok
	}
	return doJSON(ctx, pushClient(n.SkipVerify), n.URL, hdr, payload)
}

// ---------- events -> notifications ----------

// note is one notification, delivered to every channel that has its kind switched on.
type note struct {
	kind    string
	subject string
	intro   string
	facts   [][2]string
	path    string
	// ownerDevice also tells the person who owns that device (when the setting allows it).
	ownerDevice string
	urgent      bool
	tags        []string
}

func (c *Core) linkFor(path string) string {
	if path == "" {
		return ""
	}
	return strings.TrimSuffix(c.Cfg.PublicURL, "/") + path
}

// dispatch sends a note by email and push, each according to its own event toggles.
func (c *Core) dispatch(ctx context.Context, n note) {
	s := c.Settings()
	if c.EmailEnabled() && s.Email.Notify.on(n.kind) {
		c.mailNote(ctx, n)
	}
	if c.PushEnabled() && s.Notifications.Events.on(n.kind) {
		c.queuePush(pushMsg{Title: n.subject, Intro: n.intro, Facts: n.facts, Link: c.linkFor(n.path), Urgent: n.urgent, Tags: n.tags})
	}
}

// ---------- device online / offline ----------

// handleOffline tells administrators about a device that stays offline. A short grace period
// keeps a phone switching networks from sending a message every time.
func (c *Core) handleOffline(ctx context.Context, ev Event) {
	s := c.Settings()
	if !(c.EmailEnabled() && s.Email.Notify.DeviceOffline) && !(c.PushEnabled() && s.Notifications.Events.DeviceOffline) {
		return
	}
	id := str(ev.Data, "id")
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(90 * time.Second):
		}
		snap := c.Coord.Snapshot()
		d := snap.Devices[id]
		if d == nil || d.Kind == store.KindGateway || c.DeviceOnline(d) {
			return
		}
		c.offMu.Lock()
		c.offSent[id] = true
		c.offMu.Unlock()
		seen := "unknown"
		if d.LastSeenAt > 0 {
			seen = time.Unix(d.LastSeenAt, 0).UTC().Format("2 Jan 2006 15:04 UTC")
		}
		c.dispatch(ctx, note{kind: "offline", subject: "Device offline: " + d.Name,
			intro: "This device has been offline for more than a minute.",
			facts: [][2]string{{"Device", d.Name}, {"Address", d.IPv4}, {"Last seen", seen}},
			path:  "/devices/" + d.ID, tags: []string{"red_circle"}})
	}()
}

// handleOnline reports a device that is back, but only if its absence was announced.
func (c *Core) handleOnline(ctx context.Context, ev Event) {
	id := str(ev.Data, "id")
	c.offMu.Lock()
	was := c.offSent[id]
	delete(c.offSent, id)
	c.offMu.Unlock()
	if !was {
		return
	}
	d := c.Coord.Snapshot().Devices[id]
	if d == nil {
		return
	}
	c.dispatch(ctx, note{kind: "online", subject: "Device back online: " + d.Name,
		intro: "This device is connected again.",
		facts: [][2]string{{"Device", d.Name}, {"Address", d.IPv4}}, path: "/devices/" + d.ID, tags: []string{"green_circle"}})
}

// ---------- sign-in alerts ----------

type loginHistory struct{ mu sync.Mutex }

func newLoginHistory() *loginHistory { return &loginHistory{} }

// seenLogins is what an account has used before: browsers (type + system, not versions) and countries.
type seenLogins struct {
	Browsers  []string `json:"browsers"`
	Countries []string `json:"countries"`
}

func remember(list []string, v string, limit int) ([]string, bool) {
	for _, x := range list {
		if x == v {
			return list, false
		}
	}
	list = append(list, v)
	if len(list) > limit {
		list = list[len(list)-limit:]
	}
	return list, true
}

// recordLogin notes the browser and country of a sign-in and says what was new about it.
// first is true when nothing was known about the account yet.
func (c *Core) recordLogin(ctx context.Context, userID, browser, country string) (first, newBrowser, newCountry bool) {
	h := c.login
	h.mu.Lock()
	defer h.mu.Unlock()
	key := "loginseen:" + userID
	var seen seenLogins
	if err := c.Store.GetSetting(ctx, key, &seen); err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, false, false
	}
	first = len(seen.Browsers) == 0 && len(seen.Countries) == 0
	seen.Browsers, newBrowser = remember(seen.Browsers, browser, 40)
	if country != "" {
		seen.Countries, newCountry = remember(seen.Countries, country, 30)
	}
	if newBrowser || newCountry {
		_ = c.Store.PutSetting(ctx, key, seen)
	}
	return first, newBrowser, newCountry
}

func loginMethodLabel(m string) string {
	switch {
	case strings.HasPrefix(m, "sso:"):
		return "Single sign-on (" + strings.TrimPrefix(m, "sso:") + ")"
	case m == "password":
		return "Password"
	case strings.Contains(m, "passkey") || strings.Contains(m, "webauthn"):
		return "Password and passkey"
	case strings.HasPrefix(m, "password+"):
		return "Password and authenticator code"
	}
	return m
}

func (c *Core) handleLogin(ctx context.Context, ev Event) {
	s := c.Settings()
	la := s.Notifications.Login
	if la.Mode == "off" {
		return
	}
	uid, email, ip, method := str(ev.Data, "id"), str(ev.Data, "email"), str(ev.Data, "ip"), str(ev.Data, "method")
	label, browserKey := ParseUserAgent(str(ev.Data, "user_agent"))
	country := c.Geo.Country(ip)
	first, newBrowser, newCountry := c.recordLogin(ctx, uid, browserKey, country)
	isNew := newBrowser || newCountry
	if la.Mode == "new" && (first || !isNew) {
		return
	}
	reason := "You asked to be told about every sign-in."
	switch {
	case first:
		reason = "First sign-in recorded for this account."
	case newBrowser && newCountry:
		reason = "A browser and a country this account hasn't used before."
	case newBrowser:
		reason = "A browser or device this account hasn't used before."
	case newCountry:
		reason = "A country this account hasn't signed in from before."
	}
	when := time.Now().UTC().Format("2 Jan 2006 15:04 UTC")
	facts := [][2]string{{"Account", email}, {"When", when}, {"Method", loginMethodLabel(method)}, {"Browser", label}, {"Address", ip}}
	if country != "" {
		facts = append(facts, [2]string{"Country", country})
	}
	facts = append(facts, [2]string{"Why you see this", reason})
	urgent := isNew && !first

	netName := s.Network.Name
	if la.ToUser && c.EmailEnabled() {
		if u, err := c.Store.GetUser(ctx, uid); err == nil && !u.Disabled && u.Email != "" {
			intro := "Your account was just used to sign in. If that was you, there is nothing to do."
			if urgent {
				intro = "Your account was just used to sign in from a browser or country it hasn't used before. If that was you, there is nothing to do. If not, change your password and sign out all other sessions now."
			}
			c.queueMail(composeMail(netName, c.Cfg.PublicURL, []string{u.Email}, "New sign-in to "+netName, intro, facts, c.linkFor("/account")))
		}
	}
	if la.ToAdmins && c.EmailEnabled() {
		for _, r := range c.adminRecipients(ctx) {
			if strings.EqualFold(r, email) && la.ToUser {
				continue // already got the version for their own account
			}
			c.queueMail(composeMail(netName, c.Cfg.PublicURL, []string{r}, "Sign-in: "+email, "Someone signed in to the console.", facts, c.linkFor("/activity")))
		}
	}
	if la.ToPush && c.PushEnabled() {
		tags := []string{"key"}
		if urgent {
			tags = []string{"warning", "key"}
		}
		c.queuePush(pushMsg{Title: "Sign-in: " + email, Intro: "", Facts: facts, Link: c.linkFor("/activity"), Urgent: urgent, Tags: tags})
	}
}

// ParseUserAgent returns a readable name ("Chrome 131 on Windows") and a key that stays the
// same across browser updates (used to recognise a browser the account has used before).
func ParseUserAgent(ua string) (label, key string) {
	if ua == "" {
		return "Unknown", "unknown"
	}
	osName := "Unknown system"
	switch {
	case strings.Contains(ua, "Android"):
		osName = "Android"
	case strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad"):
		osName = "iOS"
	case strings.Contains(ua, "Windows"):
		osName = "Windows"
	case strings.Contains(ua, "Mac OS X") || strings.Contains(ua, "Macintosh"):
		osName = "macOS"
	case strings.Contains(ua, "CrOS"):
		osName = "ChromeOS"
	case strings.Contains(ua, "Linux") || strings.Contains(ua, "X11"):
		osName = "Linux"
	}
	name, ver := "", ""
	pick := func(token, label string) bool {
		if i := strings.Index(ua, token); i >= 0 {
			name = label
			rest := ua[i+len(token):]
			end := strings.IndexAny(rest, ". ;)")
			if end < 0 {
				end = len(rest)
			}
			ver = rest[:end]
			return true
		}
		return false
	}
	switch {
	case pick("Edg/", "Edge"), pick("OPR/", "Opera"), pick("Firefox/", "Firefox"), pick("FxiOS/", "Firefox"),
		pick("CriOS/", "Chrome"), pick("Chrome/", "Chrome"), pick("Version/", "Safari"):
	case strings.HasPrefix(ua, "curl/"):
		name = "curl"
	default:
		name = "Browser"
	}
	label = name
	if ver != "" {
		label += " " + ver
	}
	return label + " on " + osName, strings.ToLower(name + "|" + osName)
}
