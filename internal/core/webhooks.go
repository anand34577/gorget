package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/anand34577/gorget/internal/store"
)

// Webhook deliveries are signed: header X-Gorget-Signature = "t=<unix>,v1=<hex hmac-sha256(secret, t + "." + body)>".
func (c *Core) runWebhooks(ctx context.Context) {
	events, cancel := c.Bus.Subscribe(256)
	defer cancel()
	client := &http.Client{Timeout: 10 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Remote {
				continue // the instance where it happened delivers it
			}
			hooks, err := c.Store.ListWebhooks(ctx)
			if err != nil {
				continue
			}
			for _, h := range hooks {
				if !h.Enabled || !(len(h.Events) == 0 || slices.Contains(h.Events, ev.Type) || slices.Contains(h.Events, "*")) {
					continue
				}
				go c.deliver(ctx, client, h, ev)
			}
		}
	}
}

func (c *Core) deliver(ctx context.Context, client *http.Client, h store.Webhook, ev Event) {
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}
	secret, err := c.Box.Open(h.Secret)
	if err != nil {
		_ = c.Store.RecordWebhookDelivery(ctx, h.ID, 0, "cannot decrypt secret")
		return
	}
	var lastErr string
	status := 0
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(attempt*attempt) * 5 * time.Second):
			}
		}
		status, lastErr = c.post(ctx, client, h.URL, secret, body)
		if status >= 200 && status < 300 {
			lastErr = ""
			break
		}
	}
	_ = c.Store.RecordWebhookDelivery(context.WithoutCancel(ctx), h.ID, status, lastErr)
}

func (c *Core) post(ctx context.Context, client *http.Client, url, secret string, body []byte) (int, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Gorget-Webhook/"+Version)
	req.Header.Set("X-Gorget-Signature", "t="+ts+",v1="+SignWebhook(secret, ts, body))
	resp, err := client.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, ""
}

func SignWebhook(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// TestWebhook sends a test event synchronously.
func (c *Core) TestWebhook(ctx context.Context, h *store.Webhook) (int, string) {
	secret, err := c.Box.Open(h.Secret)
	if err != nil {
		return 0, err.Error()
	}
	body, _ := json.Marshal(Event{Type: "webhook.test", Time: time.Now().UTC(), Data: map[string]string{"message": "Test delivery from Gorget"}})
	status, msg := c.post(ctx, &http.Client{Timeout: 10 * time.Second}, h.URL, secret, body)
	_ = c.Store.RecordWebhookDelivery(ctx, h.ID, status, msg)
	return status, msg
}
