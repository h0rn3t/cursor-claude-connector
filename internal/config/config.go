// Package config loads runtime configuration from environment variables
// (and an optional .env file in the working directory).
package config

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Default Anthropic OAuth client id used by the official Claude CLI.
const defaultOAuthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

// Config holds all runtime configuration for the connector service.
type Config struct {
	Port                 string
	APIKey               string
	UpstashRedisRESTURL  string
	UpstashRedisRESTTok  string
	AnthropicOAuthClient string
}

// Load reads configuration from a .env file (if present) and then from
// the process environment, returning a populated Config.
func Load() (*Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return nil, fmt.Errorf("load .env: %w", err)
	}

	cfg := &Config{
		Port:                 getenv("PORT", "9095"),
		APIKey:               os.Getenv("API_KEY"),
		UpstashRedisRESTURL:  os.Getenv("UPSTASH_REDIS_REST_URL"),
		UpstashRedisRESTTok:  os.Getenv("UPSTASH_REDIS_REST_TOKEN"),
		AnthropicOAuthClient: getenv("ANTHROPIC_OAUTH_CLIENT_ID", defaultOAuthClientID),
	}

	if cfg.UpstashRedisRESTURL == "" || cfg.UpstashRedisRESTTok == "" {
		slog.Warn(
			"upstash credentials missing — OAuth storage is disabled, "+
				"the login flow will not persist tokens",
			"env", "UPSTASH_REDIS_REST_URL/UPSTASH_REDIS_REST_TOKEN",
		)
	}

	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadDotEnv loads KEY=VALUE pairs from path into the process environment.
// Existing process env values are NOT overridden. Comments and blank lines
// are ignored. Quoted values are unquoted.
func loadDotEnv(path string) error {
	f, err := os.Open(path) // #nosec G304 — config loader for known path
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		val = unquote(val)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

func unquote(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// MustGetenvInt returns the env var parsed as int, or fallback on error.
func MustGetenvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
