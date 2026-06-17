// Package store persists OAuth credentials in Upstash Redis via the
// Upstash REST API. Upstash's HTTP API is used (not RESP) so the connector
// stays compatible with both serverless and traditional deployments.
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a minimal Upstash Redis REST client. It supports the
// GET, SET, and DEL commands used by the connector.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient returns a Client configured for the given Upstash REST endpoint.
func NewClient(restURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(restURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Enabled reports whether both the REST URL and token are configured.
func (c *Client) Enabled() bool {
	return c != nil && c.BaseURL != "" && c.Token != ""
}

type upstashCmd struct {
	Result any    `json:"result"`
	Error  string `json:"error,omitempty"`
}

func (c *Client) do(ctx context.Context, command []string, path string) (*upstashCmd, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("upstash client is not configured")
	}

	endpoint := c.BaseURL + path
	body, err := json.Marshal([][]string{command})
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstash request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("upstash http %d: %s", resp.StatusCode, string(raw))
	}

	var out upstashCmd
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode upstash response: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("upstash error: %s", out.Error)
	}
	return &out, nil
}

// Get returns the raw JSON value stored at key as a string. When the key is
// missing the returned ok flag is false and the value is empty.
func (c *Client) Get(ctx context.Context, key string) (string, bool, error) {
	res, err := c.do(ctx, []string{"GET", key}, "/get/"+url.PathEscape(key))
	if err != nil {
		return "", false, err
	}
	if res.Result == nil {
		return "", false, nil
	}
	switch v := res.Result.(type) {
	case string:
		return v, true, nil
	default:
		b, err := json.Marshal(res.Result)
		if err != nil {
			return "", false, err
		}
		return string(b), true, nil
	}
}

// Set stores raw JSON at key. The value is sent as a JSON string so callers
// can store structured payloads without the REST API re-encoding them.
func (c *Client) Set(ctx context.Context, key, jsonValue string) error {
	_, err := c.do(ctx, []string{"SET", key, jsonValue}, "/set/"+url.PathEscape(key)+"?value="+url.QueryEscape(jsonValue))
	return err
}

// Del removes the key. Missing keys are not an error.
func (c *Client) Del(ctx context.Context, key string) error {
	_, err := c.do(ctx, []string{"DEL", key}, "/del/"+url.PathEscape(key))
	return err
}
