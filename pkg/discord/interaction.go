package discord

import (
	"encoding/json"
)

// InteractionType identifies the kind of interaction.
type InteractionType int

const (
	InteractionTypePing                InteractionType = 1
	InteractionTypeCommand             InteractionType = 2
	InteractionTypeComponent           InteractionType = 3
	InteractionTypeAutocomplete        InteractionType = 4
	InteractionTypeModalSubmit         InteractionType = 5
)

// InteractionContextType identifies where an interaction was triggered.
type InteractionContextType int

const (
	InteractionContextGuild          InteractionContextType = 0
	InteractionContextBotDM          InteractionContextType = 1
	InteractionContextPrivateChannel InteractionContextType = 2
)

// Interaction is the top-level object Discord sends for all interaction types.
type Interaction struct {
	ID             Snowflake          `json:"id"`
	ApplicationID  Snowflake          `json:"application_id"`
	Type           InteractionType    `json:"type"`
	Data           *InteractionData   `json:"data,omitempty"`
	GuildID        Snowflake          `json:"guild_id,omitempty"`
	ChannelID      Snowflake          `json:"channel_id,omitempty"`
	Member         *Member            `json:"member,omitempty"`
	User           *User              `json:"user,omitempty"`
	Token          string             `json:"token"`
	Version        int                `json:"version"`
	Message        *Message           `json:"message,omitempty"`
	AppPermissions string             `json:"app_permissions,omitempty"`
	Locale         string             `json:"locale,omitempty"`
	GuildLocale    string             `json:"guild_locale,omitempty"`
	Context        *InteractionContextType `json:"context,omitempty"`
}

// InteractionData carries the payload for commands, components, autocomplete, and modals.
type InteractionData struct {
	// Application command fields.
	ID       Snowflake       `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Type     CommandType     `json:"type,omitempty"`
	Resolved *ResolvedData   `json:"resolved,omitempty"`
	Options  []OptionData    `json:"options,omitempty"`
	TargetID Snowflake       `json:"target_id,omitempty"`
	GuildID  Snowflake       `json:"guild_id,omitempty"`

	// Component fields.
	CustomID      string          `json:"custom_id,omitempty"`
	ComponentType ComponentType   `json:"component_type,omitempty"`
	Values        []string        `json:"values,omitempty"`

	// Modal fields.
	Components []Component `json:"components,omitempty"`
}

// OptionData is a user-supplied value for one command option.
type OptionData struct {
	Name    string       `json:"name"`
	Type    OptionType   `json:"type"`
	Value   any          `json:"value,omitempty"`
	Options []OptionData `json:"options,omitempty"`
	Focused bool         `json:"focused,omitempty"`
}

// StringValue returns the option value as a string, or "".
func (o *OptionData) StringValue() string {
	if s, ok := o.Value.(string); ok {
		return s
	}
	return ""
}

// IntValue returns the option value as an int, or 0.
func (o *OptionData) IntValue() int {
	switch v := o.Value.(type) {
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}

// Float64Value returns the option value as a float64, or 0.
func (o *OptionData) Float64Value() float64 {
	switch v := o.Value.(type) {
	case float64:
		return v
	case json.Number:
		n, _ := v.Float64()
		return n
	}
	return 0
}

// BoolValue returns the option value as a bool, or false.
func (o *OptionData) BoolValue() bool {
	b, _ := o.Value.(bool)
	return b
}

// ResolvedData contains resolved entities referenced in command options.
type ResolvedData struct {
	Users       map[Snowflake]*User    `json:"users,omitempty"`
	Members     map[Snowflake]*Member  `json:"members,omitempty"`
	Roles       map[Snowflake]*Role    `json:"roles,omitempty"`
	Channels    map[Snowflake]*Channel `json:"channels,omitempty"`
	Messages    map[Snowflake]*Message `json:"messages,omitempty"`
	Attachments map[Snowflake]*Attachment `json:"attachments,omitempty"`
}

// ---------------------------------------------------------------------------
// Common Discord objects (minimal, relevant to interactions)
// ---------------------------------------------------------------------------

// User is a Discord user.
type User struct {
	ID            Snowflake `json:"id"`
	Username      string    `json:"username"`
	Discriminator string    `json:"discriminator"`
	GlobalName    string    `json:"global_name,omitempty"`
	Avatar        string    `json:"avatar,omitempty"`
	Bot           bool      `json:"bot,omitempty"`
}

// Member is a guild member.
type Member struct {
	User         *User     `json:"user,omitempty"`
	Nick         string    `json:"nick,omitempty"`
	Roles        []Snowflake `json:"roles,omitempty"`
	JoinedAt     string    `json:"joined_at,omitempty"`
	Permissions  string    `json:"permissions,omitempty"`
}

// Role is a guild role.
type Role struct {
	ID          Snowflake `json:"id"`
	Name        string    `json:"name"`
	Color       int       `json:"color"`
	Permissions string    `json:"permissions"`
	Mentionable bool      `json:"mentionable"`
}

// Channel is a partial channel.
type Channel struct {
	ID       Snowflake   `json:"id"`
	Name     string      `json:"name,omitempty"`
	Type     ChannelType `json:"type"`
	ParentID Snowflake   `json:"parent_id,omitempty"`
}

// Message is a partial message.
type Message struct {
	ID        Snowflake `json:"id"`
	ChannelID Snowflake `json:"channel_id"`
	Content   string    `json:"content,omitempty"`
	Author    *User     `json:"author,omitempty"`
}

// Attachment is a message attachment.
type Attachment struct {
	ID       Snowflake `json:"id"`
	Filename string    `json:"filename"`
	Size     int       `json:"size"`
	URL      string    `json:"url"`
	ProxyURL string    `json:"proxy_url,omitempty"`
}

// ---------------------------------------------------------------------------
// Interaction Responses
// ---------------------------------------------------------------------------

// CallbackType is the type of interaction response.
type CallbackType int

const (
	CallbackPong                    CallbackType = 1
	CallbackMessage                 CallbackType = 4  // CHANNEL_MESSAGE_WITH_SOURCE
	CallbackDeferredMessage         CallbackType = 5  // shows loading state
	CallbackDeferredUpdateMessage   CallbackType = 6  // for components, no loading
	CallbackUpdateMessage           CallbackType = 7  // edit the component's message
	CallbackAutocompleteResult      CallbackType = 8
	CallbackModal                   CallbackType = 9
)

// MessageFlag is a bitfield for interaction response messages.
type MessageFlag int

const (
	FlagSuppressEmbeds       MessageFlag = 1 << 2
	FlagEphemeral            MessageFlag = 1 << 6
	FlagSuppressNotifications MessageFlag = 1 << 12
)

// InteractionResponse is the payload sent to Discord's callback endpoint.
type InteractionResponse struct {
	Type CallbackType             `json:"type"`
	Data *InteractionResponseData `json:"data,omitempty"`
}

// InteractionResponseData is the message/modal/autocomplete payload.
type InteractionResponseData struct {
	// Message fields.
	TTS             bool           `json:"tts,omitempty"`
	Content         string         `json:"content,omitempty"`
	Embeds          []Embed        `json:"embeds,omitempty"`
	AllowedMentions *AllowedMentions `json:"allowed_mentions,omitempty"`
	Flags           MessageFlag    `json:"flags,omitempty"`
	Components      []Component    `json:"components,omitempty"`

	// Autocomplete fields.
	Choices []CommandOptionChoice `json:"choices,omitempty"`

	// Modal fields.
	CustomID string `json:"custom_id,omitempty"`
	Title    string `json:"title,omitempty"`
}

// Embed is a Discord rich embed.
type Embed struct {
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	URL         string       `json:"url,omitempty"`
	Color       int          `json:"color,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
}

// EmbedFooter is the footer section of an embed.
type EmbedFooter struct {
	Text string `json:"text"`
}

// EmbedField is a field within an embed.
type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// AllowedMentions controls which mentions ping.
type AllowedMentions struct {
	Parse       []string    `json:"parse,omitempty"`       // "roles", "users", "everyone"
	Roles       []Snowflake `json:"roles,omitempty"`
	Users       []Snowflake `json:"users,omitempty"`
	RepliedUser bool        `json:"replied_user,omitempty"`
}

// ---------------------------------------------------------------------------
// Response builders
// ---------------------------------------------------------------------------

// Pong creates a PONG response for a PING interaction.
func Pong() *InteractionResponse {
	return &InteractionResponse{Type: CallbackPong}
}

// MessageResponse creates a message response.
func MessageResponse(content string) *InteractionResponse {
	return &InteractionResponse{
		Type: CallbackMessage,
		Data: &InteractionResponseData{Content: content},
	}
}

// EphemeralResponse creates an ephemeral (only visible to invoker) message.
func EphemeralResponse(content string) *InteractionResponse {
	return &InteractionResponse{
		Type: CallbackMessage,
		Data: &InteractionResponseData{
			Content: content,
			Flags:   FlagEphemeral,
		},
	}
}

// DeferResponse creates a deferred response (loading state).
func DeferResponse() *InteractionResponse {
	return &InteractionResponse{Type: CallbackDeferredMessage}
}

// AutocompleteResponse creates an autocomplete result.
func AutocompleteResponse(choices ...CommandOptionChoice) *InteractionResponse {
	return &InteractionResponse{
		Type: CallbackAutocompleteResult,
		Data: &InteractionResponseData{Choices: choices},
	}
}

// ModalResponse creates a modal popup response.
func ModalResponse(customID, title string, components ...Component) *InteractionResponse {
	return &InteractionResponse{
		Type: CallbackModal,
		Data: &InteractionResponseData{
			CustomID:   customID,
			Title:      title,
			Components: components,
		},
	}
}
