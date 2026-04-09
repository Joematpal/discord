package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Token
// ---------------------------------------------------------------------------

func TestToken_Valid(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		var tok *Token
		if tok.Valid() {
			t.Error("nil token should not be valid")
		}
	})
	t.Run("empty", func(t *testing.T) {
		tok := &Token{}
		if tok.Valid() {
			t.Error("empty token should not be valid")
		}
	})
	t.Run("no expiry", func(t *testing.T) {
		tok := &Token{AccessToken: "abc"}
		if !tok.Valid() {
			t.Error("token with no expiry should be valid")
		}
	})
	t.Run("future", func(t *testing.T) {
		tok := &Token{AccessToken: "abc", ExpiresAt: time.Now().Add(time.Hour)}
		if !tok.Valid() {
			t.Error("future token should be valid")
		}
	})
	t.Run("expired", func(t *testing.T) {
		tok := &Token{AccessToken: "abc", ExpiresAt: time.Now().Add(-time.Hour)}
		if tok.Valid() {
			t.Error("expired token should not be valid")
		}
	})
}

func TestToken_Scopes(t *testing.T) {
	tok := &Token{Scope: "identify guilds bot"}
	scopes := tok.Scopes()
	if len(scopes) != 3 {
		t.Fatalf("got %d scopes, want 3", len(scopes))
	}
	if scopes[0] != "identify" || scopes[1] != "guilds" || scopes[2] != "bot" {
		t.Errorf("scopes = %v", scopes)
	}

	var nilTok *Token
	if nilTok.Scopes() != nil {
		t.Error("nil token scopes should be nil")
	}
}

// ---------------------------------------------------------------------------
// BotToken
// ---------------------------------------------------------------------------

func TestBotToken_AuthHeader(t *testing.T) {
	bt := &BotToken{Token: "MTk4NjIy.test"}
	if got := bt.AuthHeader(); got != "Bot MTk4NjIy.test" {
		t.Errorf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Config — AuthCodeURL
// ---------------------------------------------------------------------------

func TestAuthCodeURL(t *testing.T) {
	cfg := &Config{
		ClientID:    "123456",
		RedirectURI: "http://localhost:8080/callback",
		Scopes:      []string{"identify", "guilds"},
	}

	authURL, state, err := cfg.AuthCodeURL("")
	if err != nil {
		t.Fatal(err)
	}
	if state == "" {
		t.Error("auto-generated state should not be empty")
	}
	if !strings.HasPrefix(authURL, AuthorizeURL) {
		t.Errorf("URL should start with %s, got %s", AuthorizeURL, authURL)
	}

	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("client_id") != "123456" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("scope") != "identify guilds" {
		t.Errorf("scope = %q", q.Get("scope"))
	}
	if q.Get("state") != state {
		t.Error("state mismatch")
	}
	if q.Get("prompt") != "consent" {
		t.Errorf("prompt = %q", q.Get("prompt"))
	}
}

func TestAuthCodeURL_CustomState(t *testing.T) {
	cfg := &Config{
		ClientID:    "123",
		RedirectURI: "http://localhost/cb",
		Scopes:      []string{"identify"},
	}
	_, state, err := cfg.AuthCodeURL("my-custom-state")
	if err != nil {
		t.Fatal(err)
	}
	if state != "my-custom-state" {
		t.Errorf("state = %q, want my-custom-state", state)
	}
}

func TestAuthCodeURL_MissingClientID(t *testing.T) {
	cfg := &Config{RedirectURI: "http://localhost/cb"}
	_, _, err := cfg.AuthCodeURL("")
	if err != ErrNoClientID {
		t.Errorf("got %v, want ErrNoClientID", err)
	}
}

func TestAuthCodeURL_MissingRedirect(t *testing.T) {
	cfg := &Config{ClientID: "123"}
	_, _, err := cfg.AuthCodeURL("")
	if err != ErrNoRedirectURI {
		t.Errorf("got %v, want ErrNoRedirectURI", err)
	}
}

// ---------------------------------------------------------------------------
// Config — BotAuthURL
// ---------------------------------------------------------------------------

func TestBotAuthURL(t *testing.T) {
	cfg := &Config{
		ClientID: "123456",
		Scopes:   []string{"applications.commands"},
	}
	u, err := cfg.BotAuthURL(8) // send messages permission
	if err != nil {
		t.Fatal(err)
	}

	parsed, _ := url.Parse(u)
	q := parsed.Query()
	if !strings.Contains(q.Get("scope"), "bot") {
		t.Error("scope should contain 'bot'")
	}
	if !strings.Contains(q.Get("scope"), "applications.commands") {
		t.Error("scope should contain 'applications.commands'")
	}
	if q.Get("permissions") != "8" {
		t.Errorf("permissions = %q", q.Get("permissions"))
	}
}

func TestBotAuthURL_AlreadyHasBot(t *testing.T) {
	cfg := &Config{
		ClientID: "123456",
		Scopes:   []string{"bot", "identify"},
	}
	u, _ := cfg.BotAuthURL(0)
	parsed, _ := url.Parse(u)
	scope := parsed.Query().Get("scope")
	// Should not duplicate "bot".
	if strings.Count(scope, "bot") != 1 {
		t.Errorf("scope has duplicate bot: %q", scope)
	}
}

// ---------------------------------------------------------------------------
// Config — Exchange (with mock server)
// ---------------------------------------------------------------------------

func TestExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		ct := r.Header.Get("Content-Type")
		if ct != "application/x-www-form-urlencoded" {
			t.Errorf("content-type = %s", ct)
		}
		r.ParseForm()
		if r.FormValue("grant_type") != "authorization_code" {
			t.Errorf("grant_type = %s", r.FormValue("grant_type"))
		}
		if r.FormValue("code") != "test-code" {
			t.Errorf("code = %s", r.FormValue("code"))
		}
		if r.FormValue("client_id") != "myid" {
			t.Errorf("client_id = %s", r.FormValue("client_id"))
		}
		if r.FormValue("client_secret") != "mysecret" {
			t.Errorf("client_secret = %s", r.FormValue("client_secret"))
		}

		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-123",
			"token_type":    "Bearer",
			"expires_in":    604800,
			"refresh_token": "refresh-456",
			"scope":         "identify guilds",
		})
	}))
	defer srv.Close()

	cfg := &Config{
		ClientID:     "myid",
		ClientSecret: "mysecret",
		RedirectURI:  "http://localhost/cb",
		Scopes:       []string{"identify", "guilds"},
		HTTPClient:   srv.Client(),
	}
	// Override token URL by patching the request through the test server.
	origURL := TokenURL
	_ = origURL
	// We need to point at the test server. Let's use a custom transport.
	cfg.HTTPClient = &http.Client{
		Transport: &rewriteTransport{base: srv.Client().Transport, target: srv.URL},
	}

	tok, err := cfg.Exchange(context.Background(), "test-code")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "access-123" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
	if tok.RefreshToken != "refresh-456" {
		t.Errorf("RefreshToken = %q", tok.RefreshToken)
	}
	if tok.ExpiresIn != 604800 {
		t.Errorf("ExpiresIn = %d", tok.ExpiresIn)
	}
	if !tok.Valid() {
		t.Error("token should be valid")
	}
	if tok.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should be set")
	}
}

func TestExchange_MissingCode(t *testing.T) {
	cfg := &Config{ClientID: "id", ClientSecret: "secret", RedirectURI: "http://localhost/cb"}
	_, err := cfg.Exchange(context.Background(), "")
	if err != ErrNoCode {
		t.Errorf("got %v", err)
	}
}

func TestExchange_MissingSecret(t *testing.T) {
	cfg := &Config{ClientID: "id"}
	_, err := cfg.Exchange(context.Background(), "code")
	if err != ErrNoClientSecret {
		t.Errorf("got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Config — Refresh (with mock server)
// ---------------------------------------------------------------------------

func TestRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.FormValue("grant_type") != "refresh_token" {
			t.Errorf("grant_type = %s", r.FormValue("grant_type"))
		}
		if r.FormValue("refresh_token") != "old-refresh" {
			t.Errorf("refresh_token = %s", r.FormValue("refresh_token"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"token_type":    "Bearer",
			"expires_in":    604800,
			"refresh_token": "new-refresh",
			"scope":         "identify",
		})
	}))
	defer srv.Close()

	cfg := &Config{
		ClientID:     "id",
		ClientSecret: "secret",
		HTTPClient:   &http.Client{Transport: &rewriteTransport{target: srv.URL}},
	}

	tok, err := cfg.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "new-access" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
	if tok.RefreshToken != "new-refresh" {
		t.Errorf("RefreshToken = %q", tok.RefreshToken)
	}
}

func TestRefresh_MissingToken(t *testing.T) {
	cfg := &Config{ClientID: "id", ClientSecret: "secret"}
	_, err := cfg.Refresh(context.Background(), "")
	if err != ErrNoRefreshToken {
		t.Errorf("got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Config — Revoke
// ---------------------------------------------------------------------------

func TestRevoke(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.FormValue("token") != "tok-to-revoke" {
			t.Errorf("token = %s", r.FormValue("token"))
		}
		if r.FormValue("token_type_hint") != "access_token" {
			t.Errorf("hint = %s", r.FormValue("token_type_hint"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &Config{
		ClientID:     "id",
		ClientSecret: "secret",
		HTTPClient:   &http.Client{Transport: &rewriteTransport{target: srv.URL}},
	}

	err := cfg.Revoke(context.Background(), "tok-to-revoke", "access_token")
	if err != nil {
		t.Fatal(err)
	}
}

func TestRevoke_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_token"}`))
	}))
	defer srv.Close()

	cfg := &Config{
		ClientID:     "id",
		ClientSecret: "secret",
		HTTPClient:   &http.Client{Transport: &rewriteTransport{target: srv.URL}},
	}

	err := cfg.Revoke(context.Background(), "bad", "")
	if err == nil {
		t.Error("expected error for 400 response")
	}
}

// ---------------------------------------------------------------------------
// Config — ClientCredentials
// ---------------------------------------------------------------------------

func TestClientCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.FormValue("grant_type") != "client_credentials" {
			t.Errorf("grant_type = %s", r.FormValue("grant_type"))
		}
		if r.FormValue("scope") != "identify" {
			t.Errorf("scope = %s", r.FormValue("scope"))
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "cc-token",
			"token_type":   "Bearer",
			"expires_in":   604800,
			"scope":        "identify",
		})
	}))
	defer srv.Close()

	cfg := &Config{
		ClientID:     "id",
		ClientSecret: "secret",
		Scopes:       []string{"identify"},
		HTTPClient:   &http.Client{Transport: &rewriteTransport{target: srv.URL}},
	}

	tok, err := cfg.ClientCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "cc-token" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
}

// ---------------------------------------------------------------------------
// TokenSource
// ---------------------------------------------------------------------------

func TestTokenSource_Valid(t *testing.T) {
	cfg := &Config{ClientID: "id", ClientSecret: "secret"}
	tok := &Token{AccessToken: "valid", ExpiresAt: time.Now().Add(time.Hour)}
	ts := NewTokenSource(cfg, tok)

	got, err := ts.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "valid" {
		t.Errorf("AccessToken = %q", got.AccessToken)
	}
}

func TestTokenSource_AutoRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "refreshed",
			"token_type":    "Bearer",
			"expires_in":    604800,
			"refresh_token": "new-rt",
			"scope":         "identify",
		})
	}))
	defer srv.Close()

	cfg := &Config{
		ClientID:     "id",
		ClientSecret: "secret",
		HTTPClient:   &http.Client{Transport: &rewriteTransport{target: srv.URL}},
	}
	expired := &Token{
		AccessToken:  "old",
		RefreshToken: "old-rt",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}
	ts := NewTokenSource(cfg, expired)

	got, err := ts.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "refreshed" {
		t.Errorf("AccessToken = %q", got.AccessToken)
	}
}

func TestTokenSource_NoRefreshToken(t *testing.T) {
	cfg := &Config{ClientID: "id", ClientSecret: "secret"}
	expired := &Token{AccessToken: "old", ExpiresAt: time.Now().Add(-time.Hour)}
	ts := NewTokenSource(cfg, expired)

	_, err := ts.Token(context.Background())
	if err != ErrTokenExpired {
		t.Errorf("got %v, want ErrTokenExpired", err)
	}
}

func TestTokenSource_SetToken(t *testing.T) {
	cfg := &Config{ClientID: "id", ClientSecret: "secret"}
	ts := NewTokenSource(cfg, nil)
	ts.SetToken(&Token{AccessToken: "new", ExpiresAt: time.Now().Add(time.Hour)})

	got, err := ts.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "new" {
		t.Errorf("AccessToken = %q", got.AccessToken)
	}
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

func TestClient_BotToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bot my-bot-token" {
			t.Errorf("Authorization = %q", auth)
		}
		ua := r.Header.Get("User-Agent")
		if !strings.Contains(ua, "DiscordBot") {
			t.Errorf("User-Agent = %q", ua)
		}
		w.Write([]byte(`{"url":"wss://gateway.discord.gg/"}`))
	}))
	defer srv.Close()

	c := NewClient(
		WithBotToken("my-bot-token"),
		WithHTTPClient(&http.Client{Transport: &rewriteTransport{target: srv.URL}}),
	)

	resp, err := c.Get(context.Background(), "/gateway")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestClient_BearerToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer access-tok" {
			t.Errorf("Authorization = %q", auth)
		}
		w.Write([]byte(`{"id":"12345"}`))
	}))
	defer srv.Close()

	cfg := &Config{ClientID: "id", ClientSecret: "secret"}
	tok := &Token{AccessToken: "access-tok", ExpiresAt: time.Now().Add(time.Hour)}
	ts := NewTokenSource(cfg, tok)

	c := NewClient(
		WithTokenSource(ts),
		WithHTTPClient(&http.Client{Transport: &rewriteTransport{target: srv.URL}}),
	)

	resp, err := c.Get(context.Background(), "/users/@me")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestClient_CustomUserAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "MyBot/2.0" {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := NewClient(
		WithBotToken("tok"),
		WithUserAgent("MyBot/2.0"),
		WithHTTPClient(&http.Client{Transport: &rewriteTransport{target: srv.URL}}),
	)

	resp, err := c.Get(context.Background(), "/test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// ValidateState
// ---------------------------------------------------------------------------

func TestValidateState(t *testing.T) {
	if err := ValidateState("abc", "abc"); err != nil {
		t.Errorf("matching state should pass: %v", err)
	}
	if err := ValidateState("abc", "xyz"); err != ErrStateMismatch {
		t.Errorf("mismatched state: got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helper: rewrite transport to redirect requests to test server
// ---------------------------------------------------------------------------

type rewriteTransport struct {
	base   http.RoundTripper
	target string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	u, _ := url.Parse(t.target)
	req.URL.Host = u.Host
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}
