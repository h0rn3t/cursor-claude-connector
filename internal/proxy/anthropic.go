// Package proxy implements the upstream calls from the connector to
// Anthropic. It also provides a /v1/models implementation that proxies
// models.dev's open catalogue (mirroring the original behaviour).
package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Constants used to talk to the Anthropic API and to models.dev. They
// mirror what the official Anthropic SDK sends by default so requests
// look identical to the SDK's traffic.
//
// Last synchronised: 2026-06-17.
//   - @anthropic-ai/sdk 0.104.2  (latest stable, 2026-06-15)
//   - Node.js 24.x LTS  (Jod)
//   - anthropic-version 2023-06-01  (unchanged in the SDK source)
//   - anthropic-beta    oauth-2025-04-20 (OAUTH_API_BETA_HEADER, unchanged)
const (
	anthropicBase    = "https://api.anthropic.com"
	anthropicVersion = "2023-06-01"
	anthropicBeta    = "oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14"
	userAgent        = "@anthropic-ai/sdk 0.104.2 node/24.16.0"
	modelsDevURL     = "https://models.dev/api.json"
)

// Client talks to the Anthropic /v1/messages endpoint using a Bearer
// OAuth token. It does not parse responses — callers stream the body
// themselves so SSE chunks can be forwarded live.
type Client struct {
	HTTP *http.Client
}

// NewClient returns a Client with a 60s timeout. Streaming responses
// should not be impacted because the http.Response body is read in chunks
// by the caller; the timeout caps only the connect/headers phase.
func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// Send issues a POST against /v1/messages. The caller is responsible for
// closing resp.Body. isStreaming controls the Accept header.
func (c *Client) Send(ctx context.Context, token string, body []byte, isStreaming bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicBase+"/v1/messages", byteReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", anthropicBeta)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("user-agent", userAgent)
	req.Header.Set("accept-encoding", "gzip, deflate")
	if isStreaming {
		req.Header.Set("accept", "text/event-stream")
	} else {
		req.Header.Set("accept", "application/json")
	}
	return c.HTTP.Do(req)
}

type byteReadCloser struct {
	b   []byte
	pos int
}

func (r *byteReadCloser) Read(p []byte) (int, error) {
	if r.pos >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.pos:])
	r.pos += n
	return n, nil
}

func (r *byteReadCloser) Close() error { return nil }

func byteReader(b []byte) io.ReadCloser { return &byteReadCloser{b: b} }

// modelsDev is the structure used to extract the Anthropic catalogue.
type modelsDev struct {
	Anthropic struct {
		Models map[string]struct {
			ReleaseDate string `json:"release_date"`
		} `json:"models"`
	} `json:"anthropic"`
}

// ModelInfo is a single entry of the OpenAI /v1/models response.
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelsListResponse is the envelope returned by /v1/models.
type ModelsListResponse struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

// FetchModels returns the OpenAI-format model list, sourced from models.dev.
func FetchModels(ctx context.Context) (*ModelsListResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsDevURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("user-agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("models.dev request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("models.dev returned %d: %s", resp.StatusCode, string(raw))
	}

	var dev modelsDev
	if err := json.NewDecoder(resp.Body).Decode(&dev); err != nil {
		return nil, fmt.Errorf("decode models.dev: %w", err)
	}

	out := &ModelsListResponse{Object: "list"}
	for id, m := range dev.Anthropic.Models {
		ts := int64(0)
		if m.ReleaseDate != "" {
			if t, err := time.Parse("2006-01-02", m.ReleaseDate); err == nil {
				ts = t.Unix()
			}
		}
		if ts == 0 {
			ts = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
		}
		out.Data = append(out.Data, ModelInfo{
			ID:      id,
			Object:  "model",
			Created: ts,
			OwnedBy: "anthropic",
		})
	}
	// Sort newest first to mirror the original behaviour.
	for i := 0; i < len(out.Data); i++ {
		for j := i + 1; j < len(out.Data); j++ {
			if out.Data[j].Created > out.Data[i].Created {
				out.Data[i], out.Data[j] = out.Data[j], out.Data[i]
			}
		}
	}
	return out, nil
}
