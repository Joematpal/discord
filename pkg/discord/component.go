package discord

import "encoding/json"

// ComponentType identifies a message component.
type ComponentType int

const (
	ComponentActionRow     ComponentType = 1
	ComponentButton        ComponentType = 2
	ComponentStringSelect  ComponentType = 3
	ComponentTextInput     ComponentType = 4
	ComponentUserSelect    ComponentType = 5
	ComponentRoleSelect    ComponentType = 6
	ComponentMentionSelect ComponentType = 7
	ComponentChannelSelect ComponentType = 8
)

// ButtonStyle is the visual style for a button.
type ButtonStyle int

const (
	ButtonPrimary   ButtonStyle = 1
	ButtonSecondary ButtonStyle = 2
	ButtonSuccess   ButtonStyle = 3
	ButtonDanger    ButtonStyle = 4
	ButtonLink      ButtonStyle = 5
)

// TextInputStyle is the visual style for a text input.
type TextInputStyle int

const (
	TextInputShort     TextInputStyle = 1
	TextInputParagraph TextInputStyle = 2
)

// Component is a message component. Its concrete shape depends on Type.
// Using a struct with all fields (tagged omitempty) keeps JSON marshaling
// straightforward without needing interface{} gymnastics.
type Component struct {
	Type        ComponentType `json:"type"`
	CustomID    string        `json:"custom_id,omitempty"`
	Style       json.Number   `json:"style,omitempty"` // ButtonStyle or TextInputStyle
	Label       string        `json:"label,omitempty"`
	Emoji       *PartialEmoji `json:"emoji,omitempty"`
	URL         string        `json:"url,omitempty"`
	Disabled    bool          `json:"disabled,omitempty"`
	Components  []Component   `json:"components,omitempty"`
	Options     []SelectOption `json:"options,omitempty"`
	ChannelTypes []ChannelType `json:"channel_types,omitempty"`
	Placeholder string        `json:"placeholder,omitempty"`
	MinValues   *int          `json:"min_values,omitempty"`
	MaxValues   *int          `json:"max_values,omitempty"`
	MinLength   *int          `json:"min_length,omitempty"`
	MaxLength   *int          `json:"max_length,omitempty"`
	Required    bool          `json:"required,omitempty"`
	Value       string        `json:"value,omitempty"` // TextInput pre-fill / submitted value
}

// PartialEmoji is an emoji reference for buttons and select options.
type PartialEmoji struct {
	Name     string    `json:"name,omitempty"`
	ID       Snowflake `json:"id,omitempty"`
	Animated bool      `json:"animated,omitempty"`
}

// SelectOption is one choice in a string select menu.
type SelectOption struct {
	Label       string        `json:"label"`
	Value       string        `json:"value"`
	Description string        `json:"description,omitempty"`
	Emoji       *PartialEmoji `json:"emoji,omitempty"`
	Default     bool          `json:"default,omitempty"`
}

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

// ActionRow wraps child components in an action row.
func ActionRow(children ...Component) Component {
	return Component{Type: ComponentActionRow, Components: children}
}

// Button creates an interactive button.
func Button(style ButtonStyle, label, customID string) Component {
	return Component{
		Type:     ComponentButton,
		Style:    json.Number(jsonInt(int(style))),
		Label:    label,
		CustomID: customID,
	}
}

// LinkButton creates a link button (no custom_id, has URL).
func LinkButton(label, url string) Component {
	return Component{
		Type:  ComponentButton,
		Style: json.Number(jsonInt(int(ButtonLink))),
		Label: label,
		URL:   url,
	}
}

// StringSelect creates a string select menu.
func StringSelect(customID string, options ...SelectOption) Component {
	return Component{
		Type:     ComponentStringSelect,
		CustomID: customID,
		Options:  options,
	}
}

// UserSelect creates a user select menu.
func UserSelect(customID string) Component {
	return Component{Type: ComponentUserSelect, CustomID: customID}
}

// RoleSelect creates a role select menu.
func RoleSelect(customID string) Component {
	return Component{Type: ComponentRoleSelect, CustomID: customID}
}

// ChannelSelect creates a channel select menu with optional type filters.
func ChannelSelect(customID string, types ...ChannelType) Component {
	return Component{Type: ComponentChannelSelect, CustomID: customID, ChannelTypes: types}
}

// TextInput creates a text input for modals.
func TextInput(style TextInputStyle, label, customID string) Component {
	return Component{
		Type:     ComponentTextInput,
		Style:    json.Number(jsonInt(int(style))),
		Label:    label,
		CustomID: customID,
	}
}

func jsonInt(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
