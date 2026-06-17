// Package server wires all the connector's HTTP routes. It is a thin
// wrapper around the standard net/http mux — the connector doesn't need
// a third-party router.
package server

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/Maol-1997/cursor-claude-connector/internal/auth"
	"github.com/Maol-1997/cursor-claude-connector/internal/cursor"
	"github.com/Maol-1997/cursor-claude-connector/internal/proxy"
	"github.com/Maol-1997/cursor-claude-connector/internal/streaming"
)

// Server is the top-level HTTP handler.
type Server struct {
	APIKey    string
	OAuth     *auth.Manager
	Anthropic *proxy.Client
	Static    embed.FS
	// IndexPath is the slash-joined path of the HTML entry point within
	// the Static FS. With //go:embed all:public the file is "public/index.html".
	IndexPath string
	mux       *http.ServeMux
}

// New builds a Server and returns its root handler. The staticFS is
// expected to be the embed.FS produced by //go:embed all:public; the
// caller passes it as-is.
func New(apiKey string, oauth *auth.Manager, staticFS embed.FS) http.Handler {
	s := &Server{
		APIKey:    apiKey,
		OAuth:     oauth,
		Anthropic: proxy.NewClient(),
		Static:    staticFS,
		IndexPath: "public/index.html",
	}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /auth/oauth/start", s.handleOAuthStart)
	mux.HandleFunc("POST /auth/oauth/callback", s.handleOAuthCallback)
	mux.HandleFunc("POST /auth/login/start", s.handleLoginStart)
	mux.HandleFunc("GET /auth/logout", s.handleLogout)
	mux.HandleFunc("GET /auth/status", s.handleAuthStatus)

	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	mux.HandleFunc("POST /v1/messages", s.handleChat)

	mux.HandleFunc("GET /v1/models", s.handleModels)

	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /index.html", s.handleIndex)

	s.mux = mux
	return http.HandlerFunc(s.serveHTTP)
}

// serveHTTP dispatches to the configured mux.
func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// -----------------------------------------------------------------------------
// Static asset handlers
// -----------------------------------------------------------------------------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := s.Static.ReadFile(s.IndexPath)
	if err != nil {
		http.Error(w, "index not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// -----------------------------------------------------------------------------
// OAuth flow
// -----------------------------------------------------------------------------

func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	pkce, err := auth.GeneratePKCE()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to start OAuth flow", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"authUrl":   s.OAuth.AuthorizationURL(pkce),
		"sessionId": pkce.Verifier,
	})
}

func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON body", err)
		return
	}
	if strings.TrimSpace(body.Code) == "" {
		writeError(w, http.StatusBadRequest, "Missing OAuth code", nil)
		return
	}
	if _, err := s.OAuth.ExchangeCodeForTokens(r.Context(), body.Code); err != nil {
		writeError(w, http.StatusInternalServerError, "OAuth callback failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "OAuth authentication successful",
	})
}

func (s *Server) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	slog.Info("starting OAuth authentication flow")
	// There is no interactive prompt in server mode — the UI is expected
	// to call /auth/oauth/start, complete the browser flow, then call
	// /auth/oauth/callback.
	writeJSON(w, http.StatusUnauthorized, map[string]any{
		"success": false,
		"message": "Use the web UI to complete OAuth authentication.",
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.OAuth.Store.Remove(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Logged out successfully",
	})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	creds, err := s.OAuth.Store.Load(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"authenticated": creds != nil && creds.Valid(),
	})
}

// -----------------------------------------------------------------------------
// /v1/models
// -----------------------------------------------------------------------------

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	out, err := proxy.FetchModels(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Proxy error", err)
		return
	}
	sort.Slice(out.Data, func(i, j int) bool { return out.Data[i].Created > out.Data[j].Created })
	writeJSON(w, http.StatusOK, out)
}

// -----------------------------------------------------------------------------
// /v1/chat/completions and /v1/messages
// -----------------------------------------------------------------------------

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if s.APIKey != "" {
		authHeader := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(authHeader, prefix) {
			writeError(w, http.StatusUnauthorized, "Authentication required", errors.New("missing bearer token"))
			return
		}
		got := strings.TrimPrefix(authHeader, prefix)
		if got != s.APIKey {
			writeError(w, http.StatusUnauthorized, "Authentication required", errors.New("invalid API key"))
			return
		}
	}

	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Cannot read request body", err)
		return
	}

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON", err)
		return
	}

	if cursor.IsKeyCheckRequest(body) {
		payload, _ := cursor.MarshalBypass()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
		return
	}

	transform, err := proxy.TransformRequest(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Transform error", err)
		return
	}

	isStreaming, _ := body["stream"].(bool)

	upstreamBody, err := proxy.EncodeBody(body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Encode error", err)
		return
	}

	token, err := s.OAuth.AccessToken(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "OAuth error", err)
		return
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "Authentication required", errors.New(
			"please authenticate using OAuth first. Visit /auth/login for instructions"))
		return
	}

	resp, err := s.Anthropic.Send(r.Context(), token, upstreamBody, isStreaming)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Upstream error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusUnauthorized {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":   "Authentication failed",
				"message": "OAuth token may be expired. Please re-authenticate using /auth/login/start",
				"details": string(raw),
			})
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(raw)
		return
	}

	for k, vs := range resp.Header {
		lower := strings.ToLower(k)
		if lower == "content-encoding" || lower == "content-length" || lower == "transfer-encoding" {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}

	if isStreaming {
		s.streamUpstream(w, resp.Body, transform)
		return
	}
	s.nonStreamUpstream(w, resp.Body, transform)
}

func (s *Server) streamUpstream(w http.ResponseWriter, body io.Reader, transform bool) {
	flusher, _ := w.(http.Flusher)
	state := streaming.NewState()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	writeChunk := func(c streaming.OpenAIChunk) {
		buf, err := streaming.EncodeOpenAIChunk(c)
		if err != nil {
			slog.Warn("encode chunk", "err", err)
			return
		}
		if _, err := w.Write(buf); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}

	err := streaming.ScanAnthropicSSE(body, func(payload string) error {
		res := state.ProcessChunk(payload)
		if transform {
			for _, c := range res.Chunks {
				writeChunk(c)
			}
			if res.Done {
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		} else {
			if _, err := io.WriteString(w, payload+"\n\n"); err != nil {
				return err
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("stream error", "err", err)
	}
}

func (s *Server) nonStreamUpstream(w http.ResponseWriter, body io.Reader, transform bool) {
	raw, err := io.ReadAll(body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Read upstream body", err)
		return
	}
	if !transform {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
		return
	}
	var ar streaming.AnthropicResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
		return
	}
	out := streaming.ConvertNonStreaming(ar)
	w.Header().Del("Content-Encoding")
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, http.StatusOK, out)
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, payload any) {
	buf, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, fmt.Sprintf("encode error: %v", err), http.StatusInternalServerError)
		return
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}

func writeError(w http.ResponseWriter, status int, msg string, err error) {
	body := map[string]any{"error": msg}
	if err != nil {
		body["details"] = err.Error()
	}
	writeJSON(w, status, body)
}
