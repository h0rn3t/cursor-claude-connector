package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// AuthKey is the Redis key under which the Anthropic OAuth credentials are stored.
const AuthKey = "auth:anthropic"

// Credentials represents persisted Anthropic OAuth tokens.
type Credentials struct {
	Type    string `json:"type"`
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
	Expires int64  `json:"expires"`
}

// Valid reports whether the access token has not yet expired.
func (c *Credentials) Valid() bool {
	return c != nil && c.Access != "" && c.Expires > time.Now().UnixMilli()
}

// CredentialStore loads/saves Credentials via an Upstash client.
type CredentialStore struct {
	client *Client
}

// NewCredentialStore wraps an Upstash client. The client may be nil/disabled;
// in that case Load/Save/Remove return an error so the caller can surface it.
func NewCredentialStore(client *Client) *CredentialStore {
	return &CredentialStore{client: client}
}

// Enabled reports whether the underlying Upstash client is configured.
func (s *CredentialStore) Enabled() bool {
	return s != nil && s.client.Enabled()
}

// Load reads the credentials from Redis. Returns (nil, nil) when missing
// or when the underlying store is disabled.
func (s *CredentialStore) Load(ctx context.Context) (*Credentials, error) {
	if !s.Enabled() {
		return nil, nil
	}
	raw, ok, err := s.client.Get(ctx, AuthKey)
	if err != nil {
		return nil, fmt.Errorf("redis get: %w", err)
	}
	if !ok {
		return nil, nil
	}
	var c Credentials
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("decode credentials: %w", err)
	}
	return &c, nil
}

// Save persists the credentials as JSON. Returns nil (no error) when
// the store is disabled, so callers can keep the same call site.
func (s *CredentialStore) Save(ctx context.Context, c *Credentials) error {
	if !s.Enabled() {
		return nil
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	return s.client.Set(ctx, AuthKey, string(payload))
}

// Remove deletes the credentials from Redis. Returns nil (no error) when
// the store is disabled.
func (s *CredentialStore) Remove(ctx context.Context) error {
	if !s.Enabled() {
		return nil
	}
	return s.client.Del(ctx, AuthKey)
}

// LogValue returns a slog-friendly representation that omits the secrets.
func (c *Credentials) LogValue() slog.Value {
	if c == nil {
		return slog.AnyValue(nil)
	}
	return slog.GroupValue(
		slog.String("type", c.Type),
		slog.Bool("valid", c.Valid()),
		slog.Time("expires", time.UnixMilli(c.Expires)),
	)
}
