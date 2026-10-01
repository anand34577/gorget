package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// apiClient talks to the Gorget REST API with an API token (no CSRF needed for bearer tokens).
type apiClient struct {
	base  string
	token string
	hc    *http.Client
}

func newClient(url, token string, insecure bool) *apiClient {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in for self-signed lab servers
	}
	return &apiClient{base: strings.TrimRight(url, "/") + "/api/v1", token: token, hc: &http.Client{Transport: tr, Timeout: 60 * time.Second}}
}

// notFoundError is returned for HTTP 404 so resources can drop themselves from state.
type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

func isNotFound(err error) bool {
	_, ok := err.(*notFoundError)
	return ok
}

// do sends a JSON request and decodes the JSON answer into out (when non-nil).
func (c *apiClient) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Message
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		if resp.StatusCode == http.StatusNotFound {
			return &notFoundError{msg: msg}
		}
		return fmt.Errorf("%s %s: %s (HTTP %d)", method, path, msg, resp.StatusCode)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}
