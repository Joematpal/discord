// Package auth implements Discord OAuth2 and bot token authentication.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	AuthorizeURL = "https://discord.com/oauth2/authorize"
	TokenURL     = "https://discord.com/api/oauth2/token"
	RevokeURL    = "https://discord.com/api/oauth2/token/revoke"
	APIBase      = "https://discord.com/api/v10"
)

// Scopes used in Discord OAuth2.
const (
	ScopeIdentify                      = "identify"
	ScopeEmail                         = "email"
	ScopeConnections                   = "connections"
	ScopeGuilds                        = "guilds"
	ScopeGuildsJoin                    = "guilds.join"
	ScopeGuildsMembersRead             = "guilds.members.read"
	ScopeBot                           = "bot"
	ScopeApplicationsCommands          = "applications.commands"
	ScopeApplicationsCommandsUpdate    = "applications.commands.update"
	ScopeWebhookIncoming               = "webhook.incoming"
	ScopeVoice                         = "voice"
	ScopeMessagesRead                  = "messages.read"
	ScopeDMChannelsRead                = "dm_channels.read"
	ScopeRoleConnectionsWrite          = "role_connections.write"
)

var (
	ErrNoClientID     = errors.New("auth: client ID is required")
	ErrNoClientSecret = errors.New("auth: client secret is required")
	ErrNoRedirectURI  = errors.New("auth: redirect URI is required")
	ErrNoCode         = errors.New("auth: authorization code is required")
	ErrNoRefreshToken = errors.New("auth: refresh token is required")
	ErrStateMismatch  = errors.New("auth: state parameter mismatch (possible CSRF)")
	ErrTokenExpired   = errors.New("auth: token expired")
	ErrTokenExchange  = errors.New("auth: token exchange failed")
)

// Token represents an OAuth2 token response from Discord.
type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`

	// Set after token exchange, not part of JSON response.
	ExpiresAt time.Time `json:"-"`
}

// Valid reports whether the token is present and not expired.
func (t *Token) Valid() bool {
	if t == nil || t.AccessToken == "" {
		return false
	}
	if t.ExpiresAt.IsZero() {
		return true
	}
	return time.Now().Before(t.ExpiresAt)
}

// Scopes returns the individual scopes as a slice.
func (t *Token) Scopes() []string {
	if t == nil || t.Scope == "" {
		return nil
	}
	return strings.Fields(t.Scope)
}

// BotToken holds a static bot token from the Developer Portal.
type BotToken struct {
	Token string
}

// AuthHeader returns the Authorization header value for a bot token.
func (b *BotToken) AuthHeader() string {
	return "Bot " + b.Token
}

// Config holds the OAuth2 client configuration.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Scopes       []string

	// HTTPClient is optional; defaults to http.DefaultClient.
	HTTPClient *http.Client
}

func (c *Config) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *Config) validate() error {
	if c.ClientID == "" {
		return ErrNoClientID
	}
	if c.ClientSecret == "" {
		return ErrNoClientSecret
	}
	return nil
}

// AuthCodeURL builds the authorization URL for the code grant flow.
// state should be a cryptographically random string for CSRF protection;
// pass "" to auto-generate one (returned as the second value).
func (c *Config) AuthCodeURL(state string) (authURL, stateOut string, err error) {
	if c.ClientID == "" {
		return "", "", ErrNoClientID
	}
	if c.RedirectURI == "" {
		return "", "", ErrNoRedirectURI
	}

	if state == "" {
		state, err = generateState()
		if err != nil {
			return "", "", fmt.Errorf("auth: generate state: %w", err)
		}
	}

	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", c.ClientID)
	v.Set("redirect_uri", c.RedirectURI)
	v.Set("scope", strings.Join(c.Scopes, " "))
	v.Set("state", state)
	v.Set("prompt", "consent")

	return AuthorizeURL + "?" + v.Encode(), state, nil
}

// BotAuthURL builds the URL to add a bot to a guild.
// permissions is the integer bitfield of requested permissions.
func (c *Config) BotAuthURL(permissions int64) (string, error) {
	if c.ClientID == "" {
		return "", ErrNoClientID
	}

	scopes := c.Scopes
	hasBot := false
	for _, s := range scopes {
		if s == ScopeBot {
			hasBot = true
			break
		}
	}
	if !hasBot {
		scopes = append([]string{ScopeBot}, scopes...)
	}

	v := url.Values{}
	v.Set("client_id", c.ClientID)
	v.Set("scope", strings.Join(scopes, " "))
	v.Set("permissions", fmt.Sprintf("%d", permissions))

	return AuthorizeURL + "?" + v.Encode(), nil
}

// Exchange trades an authorization code for an access token.
func (c *Config) Exchange(ctx context.Context, code string) (*Token, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	if code == "" {
		return nil, ErrNoCode
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", c.RedirectURI)
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)

	return c.doTokenRequest(ctx, form)
}

// Refresh exchanges a refresh token for a new access token.
func (c *Config) Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	if refreshToken == "" {
		return nil, ErrNoRefreshToken
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)

	return c.doTokenRequest(ctx, form)
}

// ClientCredentials obtains a token using the client credentials grant.
func (c *Config) ClientCredentials(ctx context.Context, scopes ...string) (*Token, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}

	if len(scopes) == 0 {
		scopes = c.Scopes
	}

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("scope", strings.Join(scopes, " "))

	return c.doTokenRequest(ctx, form)
}

// Revoke revokes an access or refresh token.
func (c *Config) Revoke(ctx context.Context, token, tokenTypeHint string) error {
	if err := c.validate(); err != nil {
		return err
	}

	form := url.Values{}
	form.Set("token", token)
	if tokenTypeHint != "" {
		form.Set("token_type_hint", tokenTypeHint)
	}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, RevokeURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("auth: revoke failed (%d): %s", resp.StatusCode, body)
	}
	return nil
}

func (c *Config) doTokenRequest(ctx context.Context, form url.Values) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w (%d): %s", ErrTokenExchange, resp.StatusCode, body)
	}

	var tok Token
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("auth: decode token: %w", err)
	}

	if tok.ExpiresIn > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}

	return &tok, nil
}

// ---------------------------------------------------------------------------
// TokenSource — auto-refreshing token holder
// ---------------------------------------------------------------------------

// TokenSource manages a token and auto-refreshes when expired.
type TokenSource struct {
	config *Config
	mu     sync.Mutex
	token  *Token
}

// NewTokenSource creates a TokenSource with an initial token.
func NewTokenSource(config *Config, initial *Token) *TokenSource {
	return &TokenSource{config: config, token: initial}
}

// Token returns a valid token, refreshing if necessary.
func (ts *TokenSource) Token(ctx context.Context) (*Token, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.token.Valid() {
		return ts.token, nil
	}

	if ts.token == nil || ts.token.RefreshToken == "" {
		return nil, ErrTokenExpired
	}

	tok, err := ts.config.Refresh(ctx, ts.token.RefreshToken)
	if err != nil {
		return nil, err
	}

	ts.token = tok
	return tok, nil
}

// SetToken replaces the current token.
func (ts *TokenSource) SetToken(tok *Token) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.token = tok
}

// ---------------------------------------------------------------------------
// HTTP Client with automatic auth headers
// ---------------------------------------------------------------------------

// Client wraps an http.Client with automatic Authorization headers.
type Client struct {
	httpClient  *http.Client
	tokenSource *TokenSource
	botToken    *BotToken
	userAgent   string
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithTokenSource sets a Bearer token source on the client.
func WithTokenSource(ts *TokenSource) ClientOption {
	return func(c *Client) { c.tokenSource = ts }
}

// WithBotToken sets a static bot token on the client.
func WithBotToken(token string) ClientOption {
	return func(c *Client) { c.botToken = &BotToken{Token: token} }
}

// WithHTTPClient sets a custom http.Client.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) { c.httpClient = hc }
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) ClientOption {
	return func(c *Client) { c.userAgent = ua }
}

// NewClient creates an authenticated HTTP client for the Discord API.
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		httpClient: http.DefaultClient,
		userAgent:  "DiscordBot (https://github.com/joematpal/discord, 1.0)",
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Do executes an HTTP request with the appropriate Authorization header.
func (c *Client) Do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	u := path
	if !strings.HasPrefix(path, "http") {
		u = APIBase + path
	}

	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)

	if c.botToken != nil {
		req.Header.Set("Authorization", c.botToken.AuthHeader())
	} else if c.tokenSource != nil {
		tok, err := c.tokenSource.Token(ctx)
		if err != nil {
			return nil, fmt.Errorf("auth: get token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.httpClient.Do(req)
}

// Get is a convenience method for GET requests.
func (c *Client) Get(ctx context.Context, path string) (*http.Response, error) {
	return c.Do(ctx, http.MethodGet, path, nil)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ValidateState compares the state from the callback to the expected value.
func ValidateState(got, expected string) error {
	if got != expected {
		return ErrStateMismatch
	}
	return nil
}
