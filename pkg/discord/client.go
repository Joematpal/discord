package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const DefaultAPIBase = "https://discord.com/api/v10"

// CommandService manages application commands via the REST API.
// Interface-based so tests can provide a mock.
type CommandService interface {
	// Global commands.
	ListGlobalCommands(ctx context.Context) ([]Command, error)
	CreateGlobalCommand(ctx context.Context, cmd Command) (*Command, error)
	EditGlobalCommand(ctx context.Context, cmdID Snowflake, cmd Command) (*Command, error)
	DeleteGlobalCommand(ctx context.Context, cmdID Snowflake) error
	BulkOverwriteGlobalCommands(ctx context.Context, cmds []Command) ([]Command, error)

	// Guild commands.
	ListGuildCommands(ctx context.Context, guildID Snowflake) ([]Command, error)
	CreateGuildCommand(ctx context.Context, guildID Snowflake, cmd Command) (*Command, error)
	EditGuildCommand(ctx context.Context, guildID Snowflake, cmdID Snowflake, cmd Command) (*Command, error)
	DeleteGuildCommand(ctx context.Context, guildID Snowflake, cmdID Snowflake) error
	BulkOverwriteGuildCommands(ctx context.Context, guildID Snowflake, cmds []Command) ([]Command, error)
}

// InteractionClient sends follow-up messages and edits responses.
// Interface-based for testability.
type InteractionClient interface {
	// RespondToInteraction sends the initial response (within 3 seconds).
	RespondToInteraction(ctx context.Context, interactionID Snowflake, token string, resp InteractionResponse) error

	// EditOriginalResponse edits the original interaction response.
	EditOriginalResponse(ctx context.Context, token string, data InteractionResponseData) error

	// DeleteOriginalResponse deletes the original interaction response.
	DeleteOriginalResponse(ctx context.Context, token string) error

	// CreateFollowup sends a follow-up message.
	CreateFollowup(ctx context.Context, token string, data InteractionResponseData) (*Message, error)

	// EditFollowup edits a follow-up message.
	EditFollowup(ctx context.Context, token string, messageID Snowflake, data InteractionResponseData) error

	// DeleteFollowup deletes a follow-up message.
	DeleteFollowup(ctx context.Context, token string, messageID Snowflake) error
}

// Doer abstracts HTTP request execution. *http.Client satisfies this.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client implements CommandService and InteractionClient against the
// Discord REST API.
type Client struct {
	appID     Snowflake
	token     string // "Bot <token>"
	apiBase   string
	userAgent string
	http      Doer
}

// ClientConfig configures a Client.
type ClientConfig struct {
	ApplicationID Snowflake
	BotToken      string // raw token, "Bot " prefix added automatically
	APIBase       string // override for testing; defaults to DefaultAPIBase
	UserAgent     string
	HTTPClient    Doer
}

// NewClient creates a REST client for the Discord API.
func NewClient(cfg ClientConfig) *Client {
	base := cfg.APIBase
	if base == "" {
		base = DefaultAPIBase
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = "DiscordBot (https://github.com/joematpal/discord, 1.0)"
	}
	doer := cfg.HTTPClient
	if doer == nil {
		doer = http.DefaultClient
	}
	return &Client{
		appID:     cfg.ApplicationID,
		token:     "Bot " + cfg.BotToken,
		apiBase:   base,
		userAgent: ua,
		http:      doer,
	}
}

// ---------------------------------------------------------------------------
// CommandService implementation
// ---------------------------------------------------------------------------

func (c *Client) ListGlobalCommands(ctx context.Context) ([]Command, error) {
	var cmds []Command
	err := c.doJSON(ctx, http.MethodGet, c.globalCmdsPath(), nil, &cmds)
	return cmds, err
}

func (c *Client) CreateGlobalCommand(ctx context.Context, cmd Command) (*Command, error) {
	var out Command
	err := c.doJSON(ctx, http.MethodPost, c.globalCmdsPath(), cmd, &out)
	return &out, err
}

func (c *Client) EditGlobalCommand(ctx context.Context, cmdID Snowflake, cmd Command) (*Command, error) {
	var out Command
	err := c.doJSON(ctx, http.MethodPatch, c.globalCmdsPath()+"/"+cmdID.String(), cmd, &out)
	return &out, err
}

func (c *Client) DeleteGlobalCommand(ctx context.Context, cmdID Snowflake) error {
	return c.doJSON(ctx, http.MethodDelete, c.globalCmdsPath()+"/"+cmdID.String(), nil, nil)
}

func (c *Client) BulkOverwriteGlobalCommands(ctx context.Context, cmds []Command) ([]Command, error) {
	var out []Command
	err := c.doJSON(ctx, http.MethodPut, c.globalCmdsPath(), cmds, &out)
	return out, err
}

func (c *Client) ListGuildCommands(ctx context.Context, guildID Snowflake) ([]Command, error) {
	var cmds []Command
	err := c.doJSON(ctx, http.MethodGet, c.guildCmdsPath(guildID), nil, &cmds)
	return cmds, err
}

func (c *Client) CreateGuildCommand(ctx context.Context, guildID Snowflake, cmd Command) (*Command, error) {
	var out Command
	err := c.doJSON(ctx, http.MethodPost, c.guildCmdsPath(guildID), cmd, &out)
	return &out, err
}

func (c *Client) EditGuildCommand(ctx context.Context, guildID Snowflake, cmdID Snowflake, cmd Command) (*Command, error) {
	var out Command
	err := c.doJSON(ctx, http.MethodPatch, c.guildCmdsPath(guildID)+"/"+cmdID.String(), cmd, &out)
	return &out, err
}

func (c *Client) DeleteGuildCommand(ctx context.Context, guildID Snowflake, cmdID Snowflake) error {
	return c.doJSON(ctx, http.MethodDelete, c.guildCmdsPath(guildID)+"/"+cmdID.String(), nil, nil)
}

func (c *Client) BulkOverwriteGuildCommands(ctx context.Context, guildID Snowflake, cmds []Command) ([]Command, error) {
	var out []Command
	err := c.doJSON(ctx, http.MethodPut, c.guildCmdsPath(guildID), cmds, &out)
	return out, err
}

// ---------------------------------------------------------------------------
// InteractionClient implementation
// ---------------------------------------------------------------------------

func (c *Client) RespondToInteraction(ctx context.Context, interactionID Snowflake, token string, resp InteractionResponse) error {
	path := fmt.Sprintf("/interactions/%s/%s/callback", interactionID, token)
	return c.doJSON(ctx, http.MethodPost, c.apiBase+path, resp, nil)
}

func (c *Client) EditOriginalResponse(ctx context.Context, token string, data InteractionResponseData) error {
	path := fmt.Sprintf("/webhooks/%s/%s/messages/@original", c.appID, token)
	return c.doJSON(ctx, http.MethodPatch, c.apiBase+path, data, nil)
}

func (c *Client) DeleteOriginalResponse(ctx context.Context, token string) error {
	path := fmt.Sprintf("/webhooks/%s/%s/messages/@original", c.appID, token)
	return c.doJSON(ctx, http.MethodDelete, c.apiBase+path, nil, nil)
}

func (c *Client) CreateFollowup(ctx context.Context, token string, data InteractionResponseData) (*Message, error) {
	path := fmt.Sprintf("/webhooks/%s/%s", c.appID, token)
	var msg Message
	err := c.doJSON(ctx, http.MethodPost, c.apiBase+path, data, &msg)
	return &msg, err
}

func (c *Client) EditFollowup(ctx context.Context, token string, messageID Snowflake, data InteractionResponseData) error {
	path := fmt.Sprintf("/webhooks/%s/%s/messages/%s", c.appID, token, messageID)
	return c.doJSON(ctx, http.MethodPatch, c.apiBase+path, data, nil)
}

func (c *Client) DeleteFollowup(ctx context.Context, token string, messageID Snowflake) error {
	path := fmt.Sprintf("/webhooks/%s/%s/messages/%s", c.appID, token, messageID)
	return c.doJSON(ctx, http.MethodDelete, c.apiBase+path, nil, nil)
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

func (c *Client) globalCmdsPath() string {
	return fmt.Sprintf("%s/applications/%s/commands", c.apiBase, c.appID)
}

func (c *Client) guildCmdsPath(guildID Snowflake) string {
	return fmt.Sprintf("%s/applications/%s/guilds/%s/commands", c.apiBase, c.appID, guildID)
}

func (c *Client) doJSON(ctx context.Context, method, url string, reqBody, respBody any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("discord: marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.token)
	req.Header.Set("User-Agent", c.userAgent)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	if resp.StatusCode >= 400 {
		return &APIError{Status: resp.StatusCode, Body: string(body)}
	}

	if respBody != nil && len(body) > 0 {
		if err := json.Unmarshal(body, respBody); err != nil {
			return fmt.Errorf("discord: decode response: %w", err)
		}
	}
	return nil
}

// APIError is returned when the Discord API returns an error status.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("discord: API error %d: %s", e.Status, e.Body)
}
