package core

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/mail"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/anand34577/gorget/internal/mailer"
	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

const keyEmail = "email"

// EmailSettings is the SMTP account used for notifications, invitations and
// password resets.
type EmailSettings struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Security string `json:"security"` // tls | starttls | none
	Username string `json:"username"`
	// PasswordSealed is the SMTP password encrypted with the master key. It never
	// leaves the server (Redacted blanks it); PasswordSet tells the console one exists.
	PasswordSealed string `json:"password_sealed,omitempty"`
	PasswordSet    bool   `json:"password_set"`
	From           string `json:"from"`
	FromName       string `json:"from_name"`
	SkipVerify     bool   `json:"skip_verify"`
	// Recipients receive administrator notifications. Empty = every owner and admin.
	Recipients []string    `json:"recipients"`
	Notify     EmailNotify `json:"notify"`
}

// EmailNotify chooses which events send email.
type EmailNotify struct {
	DevicePending   bool `json:"device_pending"`
	DeviceAdded     bool `json:"device_added"`
	NewCountry      bool `json:"new_country"`
	DeviceBlocked   bool `json:"device_blocked"`
	KeyExpiring     bool `json:"key_expiring"`
	AccessRequests  bool `json:"access_requests"`
	RouteAdvertised bool `json:"route_advertised"`
	LoginLockout    bool `json:"login_lockout"`
	// DeviceOffline / DeviceOnline: a device has been offline for over a minute, and is back.
	DeviceOffline bool `json:"device_offline"`
	DeviceOnline  bool `json:"device_online"`
	// OwnersToo also tells people about their own devices (expiring keys, blocked, new country).
	OwnersToo bool `json:"owners_too"`
	// Invitations emails new people a link to choose their password.
	Invitations bool `json:"invitations"`
}

func defaultEmail() EmailSettings {
	return EmailSettings{
		Port: 587, Security: mailer.SecuritySTARTTLS, FromName: "Gorget", Recipients: []string{},
		Notify: EmailNotify{
			DevicePending: true, NewCountry: true, DeviceBlocked: true, KeyExpiring: true,
			AccessRequests: true, RouteAdvertised: true, LoginLockout: true, OwnersToo: true, Invitations: true,
		},
	}
}

// Redacted returns settings safe to show in the console and API.
func (s AllSettings) Redacted() AllSettings {
	s.Email.PasswordSet = s.Email.PasswordSealed != ""
	s.Email.PasswordSealed = ""
	if s.Email.Recipients == nil {
		s.Email.Recipients = []string{}
	}
	s.Notifications = s.Notifications.redacted()
	return s
}

// EmailUpdate is a change to the email settings. Password nil keeps the stored one;
// an empty string removes it.
type EmailUpdate struct {
	Settings EmailSettings
	Password *string
}

// SaveEmailSettings validates and stores the SMTP settings.
func (c *Core) SaveEmailSettings(ctx context.Context, in EmailUpdate) (AllSettings, error) {
	return c.SaveSettings(ctx, keyEmail, func(s *AllSettings) error {
		e := in.Settings
		e.PasswordSealed = s.Email.PasswordSealed
		if in.Password != nil {
			if *in.Password == "" {
				e.PasswordSealed = ""
			} else {
				sealed, err := c.Box.Seal(*in.Password)
				if err != nil {
					return err
				}
				e.PasswordSealed = sealed
			}
		}
		e.Host = strings.TrimSpace(e.Host)
		e.From = strings.TrimSpace(e.From)
		var rc []string
		for _, r := range e.Recipients {
			if r = strings.TrimSpace(r); r != "" && !slices.Contains(rc, r) {
				rc = append(rc, r)
			}
		}
		if rc == nil {
			rc = []string{}
		}
		e.Recipients = rc
		e.PasswordSet = false
		s.Email = e
		return nil
	})
}

func validateEmail(e EmailSettings) error {
	if !e.Enabled && e.Host == "" {
		return nil
	}
	if err := (mailer.Config{Host: e.Host, Port: e.Port, Security: e.Security, From: e.From, FromName: e.FromName}).Validate(); err != nil {
		return invalid("%v", err)
	}
	for _, r := range e.Recipients {
		if _, err := mail.ParseAddress(r); err != nil {
			return invalid("%q is not a valid email address", r)
		}
	}
	if len(e.Recipients) > 50 {
		return invalid("at most 50 notification recipients")
	}
	return nil
}

func (c *Core) mailConfig(e EmailSettings) (mailer.Config, error) {
	pass := ""
	if e.PasswordSealed != "" {
		p, err := c.Box.Open(e.PasswordSealed)
		if err != nil {
			return mailer.Config{}, errors.New("the stored SMTP password can't be decrypted (was the master key changed?); enter it again")
		}
		pass = p
	}
	return mailer.Config{
		Host: e.Host, Port: e.Port, Security: e.Security, Username: e.Username, Password: pass,
		From: e.From, FromName: e.FromName, SkipVerify: e.SkipVerify, HeloName: c.publicHost(),
	}, nil
}

func (c *Core) publicHost() string {
	h := strings.TrimPrefix(strings.TrimPrefix(c.Cfg.PublicURL, "https://"), "http://")
	h, _, _ = strings.Cut(h, "/")
	h, _, _ = strings.Cut(h, ":")
	return h
}

// EmailEnabled reports whether email can be sent.
func (c *Core) EmailEnabled() bool {
	e := c.Settings().Email
	return e.Enabled && e.Host != "" && e.From != ""
}

// SendTestEmail sends a message right away and returns the SMTP error, if any.
func (c *Core) SendTestEmail(ctx context.Context, to string) error {
	e := c.Settings().Email
	if e.Host == "" {
		return invalid("save the SMTP settings first")
	}
	cfg, err := c.mailConfig(e)
	if err != nil {
		return err
	}
	name := c.Settings().Network.Name
	msg := composeMail(name, c.Cfg.PublicURL, []string{to}, "Test message from "+name,
		"This is a test message from your Gorget server. If you can read it, email notifications work.",
		nil, "")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = mailer.Send(ctx, cfg, msg)
	c.mailStatus.record(err)
	return err
}

// ---------- outgoing queue ----------

type mailStatus struct {
	mu       sync.Mutex
	LastSent int64  `json:"last_sent"`
	LastErr  string `json:"last_error"`
	ErrAt    int64  `json:"last_error_at"`
	Sent     int    `json:"sent"`
	Failed   int    `json:"failed"`
}

func (m *mailStatus) record(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.LastErr, m.ErrAt = err.Error(), store.Now()
		m.Failed++
		return
	}
	m.LastSent = store.Now()
	m.Sent++
}

// MailStatus reports delivery results since the server started.
func (c *Core) MailStatus() map[string]any {
	m := &c.mailStatus
	m.mu.Lock()
	defer m.mu.Unlock()
	return map[string]any{"last_sent": m.LastSent, "last_error": m.LastErr, "last_error_at": m.ErrAt, "sent": m.Sent, "failed": m.Failed, "queued": len(c.mailq)}
}

// queueMail hands a message to the sender. It never blocks: when the queue is full
// (the SMTP server is down for a long time) the message is dropped and logged.
func (c *Core) queueMail(m mailer.Message) {
	if !c.EmailEnabled() || len(m.To) == 0 {
		return
	}
	select {
	case c.mailq <- m:
	default:
		c.Log.Warn("email queue full; message dropped", "subject", m.Subject)
	}
}

func (c *Core) runMail(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-c.mailq:
			cfg, err := c.mailConfig(c.Settings().Email)
			if err == nil {
				for attempt := 0; attempt < 3; attempt++ {
					sctx, cancel := context.WithTimeout(ctx, 45*time.Second)
					err = mailer.Send(sctx, cfg, m)
					cancel()
					if err == nil || ctx.Err() != nil {
						break
					}
					select {
					case <-ctx.Done():
					case <-time.After(time.Duration(attempt+1) * 20 * time.Second):
					}
				}
			}
			c.mailStatus.record(err)
			if err != nil {
				c.Log.Warn("email not sent", "subject", m.Subject, "err", err)
			}
		}
	}
}

// adminRecipients are the configured recipients, or every owner and admin.
func (c *Core) adminRecipients(ctx context.Context) []string {
	e := c.Settings().Email
	if len(e.Recipients) > 0 {
		return e.Recipients
	}
	users, err := c.Store.ListUsers(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, u := range users {
		if !u.Disabled && (u.Role == store.RoleOwner || u.Role == store.RoleAdmin) {
			out = append(out, u.Email)
		}
	}
	return out
}

// ownerEmail returns the email of the person a device belongs to ("" for servers).
func (c *Core) ownerEmail(ctx context.Context, deviceID string) string {
	d, err := c.Store.GetDevice(ctx, deviceID)
	if err != nil || !d.UserID.Valid {
		return ""
	}
	u, err := c.Store.GetUser(ctx, d.UserID.String)
	if err != nil || u.Disabled {
		return ""
	}
	return u.Email
}

// composeMail builds a plain and an HTML version of a short notification.
// facts are shown as a small table; link (optional) becomes a button.
func composeMail(network, publicURL string, to []string, subject, intro string, facts [][2]string, link string) mailer.Message {
	var t strings.Builder
	t.WriteString(intro + "\n\n")
	for _, f := range facts {
		fmt.Fprintf(&t, "%s: %s\n", f[0], f[1])
	}
	if link != "" {
		fmt.Fprintf(&t, "\nOpen: %s\n", link)
	}
	fmt.Fprintf(&t, "\n-- \n%s · %s\nYou get this because notifications are on for this network.\n", network, publicURL)

	var h strings.Builder
	h.WriteString(`<!doctype html><html><body style="margin:0;padding:24px;background:#f4f5f7;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#1d232b">`)
	h.WriteString(`<table role="presentation" width="100%" style="max-width:560px;margin:0 auto;background:#ffffff;border-radius:10px;border:1px solid #e3e6ea"><tr><td style="padding:24px 28px">`)
	fmt.Fprintf(&h, `<div style="font-size:12px;letter-spacing:.06em;text-transform:uppercase;color:#3A55B4;font-weight:600">%s</div>`, html.EscapeString(network))
	fmt.Fprintf(&h, `<h1 style="font-size:18px;margin:8px 0 12px">%s</h1>`, html.EscapeString(subject))
	fmt.Fprintf(&h, `<p style="font-size:14px;line-height:1.55;margin:0 0 16px">%s</p>`, html.EscapeString(intro))
	if len(facts) > 0 {
		h.WriteString(`<table role="presentation" style="font-size:13px;border-collapse:collapse;margin:0 0 18px">`)
		for _, f := range facts {
			fmt.Fprintf(&h, `<tr><td style="padding:3px 16px 3px 0;color:#66707c">%s</td><td style="padding:3px 0;font-family:ui-monospace,Menlo,Consolas,monospace">%s</td></tr>`, html.EscapeString(f[0]), html.EscapeString(f[1]))
		}
		h.WriteString(`</table>`)
	}
	if link != "" {
		fmt.Fprintf(&h, `<a href="%s" style="display:inline-block;background:#3A55B4;color:#ffffff;text-decoration:none;padding:9px 16px;border-radius:7px;font-size:14px;font-weight:600">Open in Gorget</a>`, html.EscapeString(link))
	}
	fmt.Fprintf(&h, `</td></tr></table><p style="max-width:560px;margin:14px auto 0;font-size:12px;color:#8a929c">%s · <a href="%s" style="color:#8a929c">%s</a><br>Notification settings: Settings &gt; Notifications.</p></body></html>`,
		html.EscapeString(network), html.EscapeString(publicURL), html.EscapeString(publicURL))
	return mailer.Message{To: to, Subject: subject, Text: t.String(), HTML: h.String()}
}

// mailNote emails one notification to administrators (and, with ownerDevice, to the
// device owner when the setting allows it).
func (c *Core) mailNote(ctx context.Context, n note) {
	s := c.Settings()
	to := c.adminRecipients(ctx)
	if n.ownerDevice != "" && s.Email.Notify.OwnersToo {
		if e := c.ownerEmail(ctx, n.ownerDevice); e != "" && !slices.Contains(to, e) {
			to = append(to, e)
		}
	}
	link := c.linkFor(n.path)
	// One message per recipient: people don't see each other's addresses.
	for _, r := range to {
		c.queueMail(composeMail(s.Network.Name, c.Cfg.PublicURL, []string{r}, n.subject, n.intro, n.facts, link))
	}
}

// runNotifier turns events into email and push notifications.
func (c *Core) runNotifier(ctx context.Context) {
	ch, cancel := c.Bus.Subscribe(256)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			if ev.Remote || !(c.EmailEnabled() || c.PushEnabled()) {
				continue // the instance where it happened sends the message
			}
			switch ev.Type {
			case EvLogin:
				c.handleLogin(ctx, ev)
			case EvDeviceOffline:
				c.handleOffline(ctx, ev)
			case EvDeviceOnline:
				c.handleOnline(ctx, ev)
			default:
				if n, ok := describeEvent(ev); ok {
					c.dispatch(ctx, n)
				}
			}
		}
	}
}

func str(m any, k string) string {
	switch v := m.(type) {
	case map[string]string:
		return v[k]
	case map[string]any:
		if s, ok := v[k].(string); ok {
			return s
		}
		if v[k] != nil {
			return fmt.Sprint(v[k])
		}
	}
	return ""
}

// describeEvent writes the notification for an event, if it has one.
func describeEvent(ev Event) (note, bool) {
	d := ev.Data
	switch ev.Type {
	case EvDevicePending:
		return note{kind: "pending", subject: "Device waiting for approval: " + str(d, "name"),
			intro:  "A new device signed in and is waiting for an administrator to approve it. Approve it only if you recognise it.",
			facts:  [][2]string{{"Device", str(d, "name")}, {"Address", str(d, "ipv4")}},
			path:   "/devices/" + str(d, "id"),
			urgent: true, tags: []string{"hourglass_flowing_sand"}}, true
	case EvDeviceCreated:
		if str(d, "state") == store.StatePending {
			return note{}, false
		}
		return note{kind: "added", subject: "New device joined: " + str(d, "name"),
			intro: "A device was added to your network.",
			facts: [][2]string{{"Device", str(d, "name")}, {"Type", str(d, "kind")}, {"Address", str(d, "ipv4")}},
			path:  "/devices/" + str(d, "id"), tags: []string{"new"}}, true
	case EvDeviceNewCountry:
		return note{kind: "country", subject: "Connection from a new country: " + str(d, "name"),
			intro:       "A device connected from a country it hasn't used in the last 90 days. If this wasn't expected, disable the device.",
			facts:       [][2]string{{"Device", str(d, "name")}, {"Country", str(d, "country")}, {"Public address", str(d, "public_ip")}},
			path:        "/devices/" + str(d, "id"),
			ownerDevice: str(d, "id"), urgent: true, tags: []string{"earth_africa"}}, true
	case EvDeviceBlocked:
		return note{kind: "blocked", subject: "Device blocked by security rules: " + str(d, "name"),
			intro:       "This device no longer meets your network's security rules and has lost access until it does.",
			facts:       [][2]string{{"Device", str(d, "name")}, {"Reasons", str(d, "reasons")}},
			path:        "/devices/" + str(d, "id"),
			ownerDevice: str(d, "id"), urgent: true, tags: []string{"no_entry"}}, true
	case EvDeviceKeyExpiry:
		exp := ""
		if m, ok := d.(map[string]any); ok {
			if v, ok := m["expires_at"].(int64); ok {
				exp = time.Unix(v, 0).UTC().Format("2 Jan 2006 15:04 UTC")
			}
		}
		return note{kind: "expiring", subject: "Sign-in expires soon: " + str(d, "name"),
			intro:       "This device's sign-in expires soon. Sign in again on the device to keep it connected.",
			facts:       [][2]string{{"Device", str(d, "name")}, {"Expires", exp}},
			path:        "/devices/" + str(d, "id"),
			ownerDevice: str(d, "id"), tags: []string{"alarm_clock"}}, true
	case EvAccessRequested:
		return note{kind: "access", subject: "Access request from " + str(d, "requester"),
			intro: "Someone asked for temporary access. Approve or deny it in the console.",
			facts: [][2]string{{"From", str(d, "requester")}, {"Target", str(d, "target")}, {"Ports", str(d, "ports")}, {"Minutes", str(d, "minutes")}, {"Reason", str(d, "reason")}},
			path:  "/requests", tags: []string{"raised_hand"}}, true
	case EvRouteAdvertised:
		what := str(d, "routes")
		if str(d, "exit_node") == "true" {
			what = "exit node (all internet traffic)"
		}
		return note{kind: "route", subject: "Device wants to share networks: " + str(d, "device"),
			intro: "A device offers to carry traffic for other networks. It isn't used until an administrator approves it.",
			facts: [][2]string{{"Device", str(d, "device")}, {"Offers", what}},
			path:  "/routes", tags: []string{"link"}}, true
	case EvLoginFailed:
		if str(d, "locked") != "true" {
			return note{}, false
		}
		return note{kind: "lockout", subject: "Account locked after failed sign-ins: " + str(d, "email"),
			intro: "Too many wrong passwords were entered for this account, so it is locked for a while. If it wasn't the account owner, someone may be guessing passwords.",
			facts: [][2]string{{"Account", str(d, "email")}, {"From address", str(d, "ip")}},
			path:  "/activity", urgent: true, tags: []string{"lock", "warning"}}, true
	}
	return note{}, false
}

// ---------- invitations and password resets ----------

const resetPrefix = "pwreset:"

// CreatePasswordLink makes a one-time link that lets a person choose a new password.
func (c *Core) CreatePasswordLink(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	tok := secrets.RandomToken("", 32)
	if err := c.Store.PutPending(ctx, resetPrefix+secrets.HashToken(tok), userID, int64(ttl.Seconds())); err != nil {
		return "", err
	}
	return strings.TrimSuffix(c.Cfg.PublicURL, "/") + "/reset-password#token=" + tok, nil
}

// TakePasswordToken consumes a reset token and returns the user it belongs to.
func (c *Core) TakePasswordToken(ctx context.Context, tok string) (string, bool) {
	tok = strings.TrimSpace(tok)
	if len(tok) < 20 || len(tok) > 200 {
		return "", false
	}
	id, ok, err := c.Store.TakePending(ctx, resetPrefix+secrets.HashToken(tok))
	return id, ok && err == nil
}

// SendInvitation emails a new person a link to choose their password.
func (c *Core) SendInvitation(ctx context.Context, u *store.User, invitedBy string) error {
	if !c.EmailEnabled() {
		return invalid("email isn't set up")
	}
	link, err := c.CreatePasswordLink(ctx, u.ID, 72*time.Hour)
	if err != nil {
		return err
	}
	s := c.Settings()
	msg := composeMail(s.Network.Name, c.Cfg.PublicURL, []string{u.Email}, "You're invited to "+s.Network.Name,
		invitedBy+" added you to the "+s.Network.Name+" network. Choose a password to sign in, then install the Gorget app on your devices. The link works once and expires in 3 days.",
		[][2]string{{"Your sign-in", u.Email}, {"Server", c.Cfg.PublicURL}}, link)
	c.queueMail(msg)
	return nil
}

// SendPasswordReset emails a reset link. Callers must not reveal whether the account exists.
func (c *Core) SendPasswordReset(ctx context.Context, u *store.User, ip string) error {
	link, err := c.CreatePasswordLink(ctx, u.ID, 30*time.Minute)
	if err != nil {
		return err
	}
	s := c.Settings()
	msg := composeMail(s.Network.Name, c.Cfg.PublicURL, []string{u.Email}, "Reset your "+s.Network.Name+" password",
		"Someone (hopefully you) asked to reset the password for this account. The link works once and expires in 30 minutes. If you didn't ask, ignore this email; your password stays the same.",
		[][2]string{{"Account", u.Email}, {"Requested from", ip}}, link)
	c.queueMail(msg)
	return nil
}
