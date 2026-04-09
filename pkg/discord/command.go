package discord

// Application Command types per Discord API.
// https://discord.com/developers/docs/interactions/application-commands

// CommandType is the type of application command.
type CommandType int

const (
	CommandTypeChatInput       CommandType = 1 // Slash command (/ in chat)
	CommandTypeUser            CommandType = 2 // Right-click user context menu
	CommandTypeMessage         CommandType = 3 // Right-click message context menu
	CommandTypePrimaryEntry    CommandType = 4 // Primary entry point for Activities
)

// OptionType is the type of a command option.
type OptionType int

const (
	OptionTypeSubCommand      OptionType = 1
	OptionTypeSubCommandGroup OptionType = 2
	OptionTypeString          OptionType = 3
	OptionTypeInteger         OptionType = 4
	OptionTypeBoolean         OptionType = 5
	OptionTypeUser            OptionType = 6
	OptionTypeChannel         OptionType = 7
	OptionTypeRole            OptionType = 8
	OptionTypeMentionable     OptionType = 9
	OptionTypeNumber          OptionType = 10
	OptionTypeAttachment      OptionType = 11
)

// ChannelType constrains which channel types appear in a CHANNEL option.
type ChannelType int

const (
	ChannelTypeGuildText          ChannelType = 0
	ChannelTypeDM                 ChannelType = 1
	ChannelTypeGuildVoice         ChannelType = 2
	ChannelTypeGroupDM            ChannelType = 3
	ChannelTypeGuildCategory      ChannelType = 4
	ChannelTypeGuildAnnouncement  ChannelType = 5
	ChannelTypeAnnouncementThread ChannelType = 10
	ChannelTypePublicThread       ChannelType = 11
	ChannelTypePrivateThread      ChannelType = 12
	ChannelTypeGuildStageVoice    ChannelType = 13
	ChannelTypeGuildForum         ChannelType = 15
	ChannelTypeGuildMedia         ChannelType = 16
)

// Command is the payload for creating/updating an application command.
type Command struct {
	ID                       Snowflake          `json:"id,omitempty"`
	Type                     CommandType        `json:"type,omitempty"`
	ApplicationID            Snowflake          `json:"application_id,omitempty"`
	GuildID                  Snowflake          `json:"guild_id,omitempty"`
	Name                     string             `json:"name"`
	NameLocalizations        map[string]string  `json:"name_localizations,omitempty"`
	Description              string             `json:"description"`
	DescriptionLocalizations map[string]string  `json:"description_localizations,omitempty"`
	Options                  []CommandOption    `json:"options,omitempty"`
	DefaultMemberPermissions *string            `json:"default_member_permissions,omitempty"`
	NSFW                     bool               `json:"nsfw,omitempty"`
	IntegrationTypes         []int              `json:"integration_types,omitempty"`
	Contexts                 []int              `json:"contexts,omitempty"`
	Version                  Snowflake          `json:"version,omitempty"`
}

// CommandOption is one parameter/subcommand within a Command.
type CommandOption struct {
	Type                     OptionType             `json:"type"`
	Name                     string                 `json:"name"`
	NameLocalizations        map[string]string      `json:"name_localizations,omitempty"`
	Description              string                 `json:"description"`
	DescriptionLocalizations map[string]string      `json:"description_localizations,omitempty"`
	Required                 bool                   `json:"required,omitempty"`
	Choices                  []CommandOptionChoice   `json:"choices,omitempty"`
	Options                  []CommandOption         `json:"options,omitempty"`
	ChannelTypes             []ChannelType           `json:"channel_types,omitempty"`
	MinValue                 *float64               `json:"min_value,omitempty"`
	MaxValue                 *float64               `json:"max_value,omitempty"`
	MinLength                *int                   `json:"min_length,omitempty"`
	MaxLength                *int                   `json:"max_length,omitempty"`
	Autocomplete             bool                   `json:"autocomplete,omitempty"`
}

// CommandOptionChoice is a predefined choice for a STRING, INTEGER, or NUMBER option.
type CommandOptionChoice struct {
	Name              string            `json:"name"`
	NameLocalizations map[string]string `json:"name_localizations,omitempty"`
	Value             any               `json:"value"` // string | int | float64
}

// ---------------------------------------------------------------------------
// Builders — fluent API for constructing commands
// ---------------------------------------------------------------------------

// NewSlashCommand starts building a CHAT_INPUT (slash) command.
func NewSlashCommand(name, description string) *Command {
	return &Command{
		Type:        CommandTypeChatInput,
		Name:        name,
		Description: description,
	}
}

// NewUserCommand starts building a USER context-menu command.
func NewUserCommand(name string) *Command {
	return &Command{
		Type:        CommandTypeUser,
		Name:        name,
		Description: "",
	}
}

// NewMessageCommand starts building a MESSAGE context-menu command.
func NewMessageCommand(name string) *Command {
	return &Command{
		Type:        CommandTypeMessage,
		Name:        name,
		Description: "",
	}
}

// AddOption appends an option to the command.
func (c *Command) AddOption(opt CommandOption) *Command {
	c.Options = append(c.Options, opt)
	return c
}

// StringOption creates a string option.
func StringOption(name, description string, required bool) CommandOption {
	return CommandOption{Type: OptionTypeString, Name: name, Description: description, Required: required}
}

// IntegerOption creates an integer option.
func IntegerOption(name, description string, required bool) CommandOption {
	return CommandOption{Type: OptionTypeInteger, Name: name, Description: description, Required: required}
}

// BooleanOption creates a boolean option.
func BooleanOption(name, description string, required bool) CommandOption {
	return CommandOption{Type: OptionTypeBoolean, Name: name, Description: description, Required: required}
}

// UserOption creates a user option.
func UserOption(name, description string, required bool) CommandOption {
	return CommandOption{Type: OptionTypeUser, Name: name, Description: description, Required: required}
}

// ChannelOption creates a channel option with optional type constraints.
func ChannelOption(name, description string, required bool, types ...ChannelType) CommandOption {
	return CommandOption{Type: OptionTypeChannel, Name: name, Description: description, Required: required, ChannelTypes: types}
}

// RoleOption creates a role option.
func RoleOption(name, description string, required bool) CommandOption {
	return CommandOption{Type: OptionTypeRole, Name: name, Description: description, Required: required}
}

// NumberOption creates a floating-point number option.
func NumberOption(name, description string, required bool) CommandOption {
	return CommandOption{Type: OptionTypeNumber, Name: name, Description: description, Required: required}
}

// SubCommand creates a subcommand option.
func SubCommand(name, description string, opts ...CommandOption) CommandOption {
	return CommandOption{Type: OptionTypeSubCommand, Name: name, Description: description, Options: opts}
}

// SubCommandGroup creates a subcommand group option.
func SubCommandGroup(name, description string, subs ...CommandOption) CommandOption {
	return CommandOption{Type: OptionTypeSubCommandGroup, Name: name, Description: description, Options: subs}
}

// WithChoices adds choices to an option. Returns a copy.
func (o CommandOption) WithChoices(choices ...CommandOptionChoice) CommandOption {
	o.Choices = choices
	return o
}

// Choice creates a CommandOptionChoice.
func Choice(name string, value any) CommandOptionChoice {
	return CommandOptionChoice{Name: name, Value: value}
}
