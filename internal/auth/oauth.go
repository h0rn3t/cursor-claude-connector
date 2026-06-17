// Package auth implements the Anthropic OAuth flow used by the connector.
// The same flow is used by Anthropic's official Claude CLI: the OAuth
// authorize endpoint echoes the PKCE verifier after a `#` in the redirect
// code, which we recover client-side. The HTTP-based start/refresh uses the
// well-known client id of the official Claude CLI.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Maol-1997/cursor-claude-connector/internal/store"
)

// Public Anthropic OAuth endpoints.
var (
	authorizeURL   = "https://claude.ai/oauth/authorize"
	tokenURL       = "https://console.anthropic.com/v1/oauth/token"
	redirectURI    = "https://console.anthropic.com/oauth/code/callback"
	defaultScope   = "org:create_api_key user:profile user:inference"
	tokenClockSkew = 30 * time.Second
)

// PKCE is a verifier/challenge pair for the OAuth Authorization Code flow.
type PKCE struct {
	Verifier  string
	Challenge string
}

// Manager coordinates the OAuth flow and the credential store.
type Manager struct {
	ClientID string
	Store    *store.CredentialStore
	HTTP     *http.Client
}

// NewManager builds a Manager with sane HTTP defaults.
func NewManager(clientID string, st *store.CredentialStore) *Manager {
	return &Manager{
		ClientID: clientID,
		Store:    st,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
	}
}

// GeneratePKCE returns a fresh PKCE pair.
func GeneratePKCE() (PKCE, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return PKCE{}, fmt.Errorf("rand: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)

	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	return PKCE{Verifier: verifier, Challenge: challenge}, nil
}

// AuthorizationURL builds the URL the user should open in a browser to
// authenticate. The state parameter carries the PKCE verifier so it is
// echoed back in the redirect and we can validate it without a session store.
func (m *Manager) AuthorizationURL(pkce PKCE) string {
	q := url.Values{}
	q.Set("code", "true")
	q.Set("client_id", m.ClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", defaultScope)
	q.Set("code_challenge", pkce.Challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", pkce.Verifier)
	return authorizeURL + "?" + q.Encode()
}

// TokenResponse mirrors the fields used from the OAuth token endpoint.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// ExchangeCodeForTokens exchanges an authorization code for tokens and
// persists them. The code parameter is allowed to be of the form
// "code#verifier" — that is how Claude returns it after the redirect.
func (m *Manager) ExchangeCodeForTokens(ctx context.Context, code string) (*TokenResponse, error) {
	splits := strings.SplitN(code, "#", 2)
	authCode := splits[0]
	verifier := ""
	if len(splits) > 1 {
		verifier = splits[1]
	}

	body, _ := json.Marshal(map[string]string{
		"code":          authCode,
		"state":         verifier,
		"grant_type":    "authorization_code",
		"client_id":     m.ClientID,
		"redirect_uri":  redirectURI,
		"code_verifier": verifier,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(raw))
	}

	var tr TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}

	creds := &store.Credentials{
		Type:    "oauth",
		Access:  tr.AccessToken,
		Refresh: tr.RefreshToken,
		Expires: time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second - tokenClockSkew).UnixMilli(),
	}
	if err := m.Store.Save(ctx, creds); err != nil {
		return nil, fmt.Errorf("persist tokens: %w", err)
	}
	slog.Info("oauth tokens saved", "expires_in", tr.ExpiresIn)
	return &tr, nil
}

// Refresh exchanges the stored refresh token for a fresh access token.
func (m *Manager) Refresh(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     m.ClientID,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("refresh endpoint returned %d: %s", resp.StatusCode, string(raw))
	}

	var tr TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, fmt.Errorf("decode refresh response: %w", err)
	}
	return &tr, nil
}

// AccessToken returns a valid access token, refreshing it transparently
// when expired. Returns ("", nil) when no credentials are persisted.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	creds, err := m.Store.Load(ctx)
	if err != nil {
		return "", err
	}
	if creds == nil {
		return "", nil
	}
	if creds.Valid() {
		return creds.Access, nil
	}
	if creds.Refresh == "" {
		return "", nil
	}
	slog.Info("oauth token expired, refreshing")
	tr, err := m.Refresh(ctx, creds.Refresh)
	if err != nil {
		return "", err
	}
	newCreds := &store.Credentials{
		Type:    "oauth",
		Access:  tr.AccessToken,
		Refresh: tr.RefreshToken,
		Expires: time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second - tokenClockSkew).UnixMilli(),
	}
	if err := m.Store.Save(ctx, newCreds); err != nil {
		return "", err
	}
	return tr.AccessToken, nil
}
