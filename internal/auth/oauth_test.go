package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Maol-1997/cursor-claude-connector/internal/store"
)

func TestGeneratePKCE(t *testing.T) {
	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if pkce.Verifier == "" || pkce.Challenge == "" {
		t.Fatal("PKCE fields should be non-empty")
	}
	if pkce.Verifier == pkce.Challenge {
		t.Fatal("verifier and challenge must differ")
	}
}

func TestAuthorizationURL(t *testing.T) {
	cs := store.NewCredentialStore(nil)
	mgr := NewManager("test-client", cs)
	pkce, _ := GeneratePKCE()
	url := mgr.AuthorizationURL(pkce)
	if !strings.Contains(url, "client_id=test-client") {
		t.Errorf("URL missing client_id: %s", url)
	}
	if !strings.Contains(url, "code_challenge="+pkce.Challenge) {
		t.Errorf("URL missing PKCE challenge: %s", url)
	}
	if !strings.Contains(url, "code_challenge_method=S256") {
		t.Errorf("URL missing S256: %s", url)
	}
	if !strings.Contains(url, "state="+pkce.Verifier) {
		t.Errorf("URL missing state=verifier: %s", url)
	}
}

// WithTokenURL replaces the package-level token endpoint with a custom
// one. It returns a cleanup function the caller must defer.
func withTokenURL(t *testing.T, server *httptest.Server) {
	t.Helper()
	prev := tokenURL
	tokenURL = server.URL
	t.Cleanup(func() { tokenURL = prev })
}

func TestExchangeCodeForTokens_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("want POST, got %s", r.Method)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"abc","refresh_token":"def","expires_in":3600}`))
	}))
	defer srv.Close()
	withTokenURL(t, srv)

	cs := store.NewCredentialStore(nil)
	mgr := NewManager("test-client", cs)
	resp, err := mgr.ExchangeCodeForTokens(t.Context(), "authcode#verifier")
	if err != nil {
		t.Fatal(err)
	}
	if resp.AccessToken != "abc" || resp.RefreshToken != "def" {
		t.Errorf("unexpected response: %+v", resp)
	}
}
