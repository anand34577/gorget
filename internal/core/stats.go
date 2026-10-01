package core

import (
	"context"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/anand34577/gorget/internal/secrets"
	"github.com/anand34577/gorget/internal/store"
)

// How often presence is checked, how often the network is sampled, and how long
// history is kept.
const (
	presenceEvery  = 30 * time.Second
	sampleEvery    = 5 * time.Minute
	keepSamples    = 90 * 24 * time.Hour
	keepSessions   = 180 * 24 * time.Hour
	newCountryDays = 90
)

type openSession struct {
	id      string
	ip      string
	started int64
}

// runStats records when devices connect and disconnect (with the address they use)
// and samples network-wide numbers for the charts. One instance does this.
func (c *Core) runStats(ctx context.Context) {
	open := map[string]openSession{}
	blocked := map[string]bool{}
	var prevRx, prevTx int64
	first := true
	lastSample := time.Time{}
	lastPrune := time.Time{}
	wasLeader := false

	t := time.NewTicker(presenceEvery)
	defer t.Stop()
	for {
		if c.IsLeader() {
			if !wasLeader {
				// Sessions left open by a previous leader or process can't be trusted.
				_ = c.Store.CloseOpenSessions(ctx, store.Now())
				open = map[string]openSession{}
				first = true
				wasLeader = true
			}
			c.trackPresence(ctx, open)
			c.trackBlocked(blocked, first)
			if time.Since(lastSample) >= sampleEvery {
				rx, tx := c.sample(ctx, prevRx, prevTx, first)
				prevRx, prevTx, first = rx, tx, false
				lastSample = time.Now()
			}
			if time.Since(lastPrune) > 6*time.Hour {
				now := time.Now()
				_ = c.Store.PruneHistory(ctx, now.Add(-keepSamples).Unix(), now.Add(-keepSessions).Unix())
				lastPrune = now
			}
		} else {
			wasLeader = false
		}
		select {
		case <-ctx.Done():
			// Record the end of every session we know about.
			now := store.Now()
			for _, s := range open {
				_ = c.Store.CloseSession(context.Background(), s.id, now)
			}
			return
		case <-t.C:
		}
	}
}

// RemoteIP is the public address a device connects from.
func RemoteIP(d *store.Device) string {
	if d.Kind == store.KindWireGuard {
		if h, _, err := net.SplitHostPort(d.LastEndpoint); err == nil {
			return h
		}
		return ""
	}
	return d.PublicIP
}

func (c *Core) trackPresence(ctx context.Context, open map[string]openSession) {
	devs, err := c.Store.ListDevices(ctx)
	if err != nil {
		return
	}
	now := store.Now()
	seen := map[string]bool{}
	for i := range devs {
		d := &devs[i]
		if d.Kind == store.KindGateway {
			continue
		}
		seen[d.ID] = true
		online := c.DeviceOnline(d)
		ip := RemoteIP(d)
		cur, isOpen := open[d.ID]
		if isOpen && (!online || (ip != "" && ip != cur.ip)) {
			_ = c.Store.CloseSession(ctx, cur.id, now)
			delete(open, d.ID)
			isOpen = false
		}
		if online && !isOpen {
			country := c.Geo.Country(ip)
			s := &store.DeviceSession{ID: secrets.RandomID(), DeviceID: d.ID, StartedAt: now, PublicIP: ip, Country: country, ClientVersion: d.ClientVersion}
			if country != "" {
				known, _ := c.Store.KnownCountries(ctx, d.ID, time.Now().AddDate(0, 0, -newCountryDays).Unix())
				// The first country a device ever uses isn't news; a change is.
				if len(known) > 0 && !containsStr(known, country) {
					c.Bus.Publish(EvDeviceNewCountry, map[string]any{"id": d.ID, "name": d.Name, "country": country, "public_ip": ip, "previous": strings.Join(known, ", ")})
					c.Audit(ctx, SystemActor, "device.new_country", "device", d.ID, d.Name, map[string]any{"country": country, "ip": ip, "previous": known})
				}
			}
			if err := c.Store.OpenSession(ctx, s); err == nil {
				open[d.ID] = openSession{id: s.ID, ip: ip, started: now}
			}
		}
	}
	for id := range open {
		if !seen[id] { // device deleted (its sessions are deleted with it)
			delete(open, id)
		}
	}
}

// trackBlocked announces devices that newly fail enforced security rules.
func (c *Core) trackBlocked(blocked map[string]bool, first bool) {
	snap := c.Coord.Snapshot()
	if !snap.Settings.Posture.Enabled || snap.Settings.Posture.Mode != PostureEnforce {
		clear(blocked)
		return
	}
	now := map[string]bool{}
	for id, reasons := range snap.Posture {
		if len(reasons) == 0 {
			continue
		}
		now[id] = true
		if !blocked[id] && !first {
			if d := snap.Devices[id]; d != nil {
				c.Bus.Publish(EvDeviceBlocked, map[string]any{"id": id, "name": d.Name, "reasons": strings.Join(reasons, "; ")})
			}
		}
	}
	clear(blocked)
	for id := range now {
		blocked[id] = true
	}
}

func containsStr(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// sample stores one network-wide data point. Traffic counters on devices restart
// when a device reconnects, so only increases count.
func (c *Core) sample(ctx context.Context, prevRx, prevTx int64, first bool) (int64, int64) {
	devs, err := c.Store.ListDevices(ctx)
	if err != nil {
		return prevRx, prevTx
	}
	x := &store.StatsSample{TS: time.Now().Truncate(time.Minute).Unix()}
	var rx, tx int64
	for i := range devs {
		d := &devs[i]
		if d.Kind == store.KindGateway {
			continue
		}
		x.Total++
		switch d.State {
		case store.StatePending:
			x.Pending++
		case store.StateDisabled:
			x.Disabled++
		}
		if c.DeviceOnline(d) {
			x.Online++
		}
		rx += d.RxBytes
		tx += d.TxBytes
	}
	if !first {
		x.Rx, x.Tx = max(rx-prevRx, 0), max(tx-prevTx, 0)
	}
	_ = c.Store.InsertSample(ctx, x)
	return rx, tx
}

// ---------- reading statistics ----------

// StatsPoint is one point of a chart.
type StatsPoint struct {
	TS      int64 `json:"ts"`
	Online  int   `json:"online"`
	Total   int   `json:"total"`
	Pending int   `json:"pending"`
	Rx      int64 `json:"rx"`
	Tx      int64 `json:"tx"`
}

// StatsSeries returns samples since `since`, merged into at most `points` buckets
// (online and total use the bucket's peak; traffic is summed).
func (c *Core) StatsSeries(ctx context.Context, since time.Time, points int) ([]StatsPoint, error) {
	samples, err := c.Store.Samples(ctx, since.Unix())
	if err != nil {
		return nil, err
	}
	if points <= 0 {
		points = 120
	}
	span := time.Since(since).Seconds()
	bucket := int64(span) / int64(points)
	if bucket < int64(sampleEvery.Seconds()) {
		bucket = int64(sampleEvery.Seconds())
	}
	out := []StatsPoint{}
	for _, s := range samples {
		ts := s.TS - s.TS%bucket
		if n := len(out); n > 0 && out[n-1].TS == ts {
			p := &out[n-1]
			p.Online, p.Total, p.Pending = max(p.Online, s.Online), max(p.Total, s.Total), max(p.Pending, s.Pending)
			p.Rx += s.Rx
			p.Tx += s.Tx
			continue
		}
		out = append(out, StatsPoint{TS: ts, Online: s.Online, Total: s.Total, Pending: s.Pending, Rx: s.Rx, Tx: s.Tx})
	}
	return out, nil
}

// CountRow is a label with a count (for breakdown charts).
type CountRow struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

func countRows(m map[string]int) []CountRow {
	out := make([]CountRow, 0, len(m))
	for k, v := range m {
		out = append(out, CountRow{Label: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// Breakdown counts devices by operating system, client version, country and state.
func (c *Core) Breakdown(devs []store.Device) map[string][]CountRow {
	os, ver, country, state := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	for i := range devs {
		d := &devs[i]
		if d.Kind == store.KindGateway {
			continue
		}
		o := d.OS
		if d.Kind == store.KindWireGuard {
			o = "wireguard"
		}
		if o == "" {
			o = "unknown"
		}
		os[o]++
		if d.Kind == store.KindNative && d.ClientVersion != "" {
			ver[d.ClientVersion]++
		}
		if cc := c.Geo.Country(RemoteIP(d)); cc != "" {
			country[cc]++
		}
		st := d.State
		if st == store.StateActive {
			if c.DeviceOnline(d) {
				st = "online"
			} else {
				st = "offline"
			}
		}
		state[st]++
	}
	return map[string][]CountRow{"os": countRows(os), "versions": countRows(ver), "countries": countRows(country), "states": countRows(state)}
}

// ---------- country database ----------

// runGeoUpdates keeps the free country database current when the admin turned
// automatic updates on (checked daily, downloaded when older than a month).
func (c *Core) runGeoUpdates(ctx context.Context) {
	t := time.NewTicker(24 * time.Hour)
	defer t.Stop()
	check := func() {
		if !c.Settings().Posture.GeoAutoUpdate || !c.IsLeader() {
			return
		}
		if c.Geo.Loaded() && c.Geo.Age() < 35*24*time.Hour {
			return
		}
		if err := c.UpdateGeoDB(ctx); err != nil {
			c.Log.Warn("country database update failed", "err", err)
		}
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Minute):
	}
	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			check()
		}
	}
}

// UpdateGeoDB downloads the latest free country database now.
func (c *Core) UpdateGeoDB(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := c.Geo.Download(ctx, &http.Client{Timeout: 4 * time.Minute}); err != nil {
		return err
	}
	c.Log.Info("country database loaded", "file", c.Geo.Status().File)
	c.Coord.Trigger() // country rules may now give different answers
	return nil
}
