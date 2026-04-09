package discord

import (
	"encoding/json"
	"testing"
)

func TestNewSlashCommand(t *testing.T) {
	cmd := NewSlashCommand("ping", "Check if bot is alive")
	if cmd.Type != CommandTypeChatInput {
		t.Errorf("Type = %d", cmd.Type)
	}
	if cmd.Name != "ping" {
		t.Errorf("Name = %q", cmd.Name)
	}
}

func TestCommand_AddOption(t *testing.T) {
	cmd := NewSlashCommand("greet", "Greet someone").
		AddOption(UserOption("user", "Who to greet", true)).
		AddOption(StringOption("message", "Custom message", false))

	if len(cmd.Options) != 2 {
		t.Fatalf("got %d options", len(cmd.Options))
	}
	if cmd.Options[0].Type != OptionTypeUser {
		t.Errorf("opt 0 type = %d", cmd.Options[0].Type)
	}
	if !cmd.Options[0].Required {
		t.Error("opt 0 should be required")
	}
	if cmd.Options[1].Type != OptionTypeString {
		t.Errorf("opt 1 type = %d", cmd.Options[1].Type)
	}
}

func TestOptionBuilders(t *testing.T) {
	tests := []struct {
		name string
		opt  CommandOption
		typ  OptionType
	}{
		{"string", StringOption("s", "d", false), OptionTypeString},
		{"integer", IntegerOption("i", "d", false), OptionTypeInteger},
		{"boolean", BooleanOption("b", "d", false), OptionTypeBoolean},
		{"user", UserOption("u", "d", false), OptionTypeUser},
		{"channel", ChannelOption("c", "d", false, ChannelTypeGuildText), OptionTypeChannel},
		{"role", RoleOption("r", "d", false), OptionTypeRole},
		{"number", NumberOption("n", "d", false), OptionTypeNumber},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.opt.Type != tt.typ {
				t.Errorf("Type = %d, want %d", tt.opt.Type, tt.typ)
			}
		})
	}
}

func TestChannelOption_Types(t *testing.T) {
	opt := ChannelOption("ch", "Pick", false, ChannelTypeGuildText, ChannelTypeGuildVoice)
	if len(opt.ChannelTypes) != 2 {
		t.Fatalf("got %d types", len(opt.ChannelTypes))
	}
}

func TestSubCommand(t *testing.T) {
	sub := SubCommand("add", "Add item", StringOption("name", "Item name", true))
	if sub.Type != OptionTypeSubCommand {
		t.Errorf("Type = %d", sub.Type)
	}
	if len(sub.Options) != 1 {
		t.Fatalf("sub options = %d", len(sub.Options))
	}
}

func TestSubCommandGroup(t *testing.T) {
	grp := SubCommandGroup("items", "Manage items",
		SubCommand("add", "Add item"),
		SubCommand("remove", "Remove item"),
	)
	if grp.Type != OptionTypeSubCommandGroup {
		t.Errorf("Type = %d", grp.Type)
	}
	if len(grp.Options) != 2 {
		t.Fatalf("group subs = %d", len(grp.Options))
	}
}

func TestWithChoices(t *testing.T) {
	opt := StringOption("color", "Pick a color", true).WithChoices(
		Choice("Red", "red"),
		Choice("Blue", "blue"),
	)
	if len(opt.Choices) != 2 {
		t.Fatalf("choices = %d", len(opt.Choices))
	}
	if opt.Choices[0].Name != "Red" {
		t.Errorf("choice 0 = %q", opt.Choices[0].Name)
	}
}

func TestNewUserCommand(t *testing.T) {
	cmd := NewUserCommand("Report User")
	if cmd.Type != CommandTypeUser {
		t.Errorf("Type = %d", cmd.Type)
	}
	if cmd.Description != "" {
		t.Error("user commands should have empty description")
	}
}

func TestNewMessageCommand(t *testing.T) {
	cmd := NewMessageCommand("Bookmark")
	if cmd.Type != CommandTypeMessage {
		t.Errorf("Type = %d", cmd.Type)
	}
}

func TestCommand_JSON(t *testing.T) {
	cmd := NewSlashCommand("echo", "Echo a message").
		AddOption(StringOption("text", "What to echo", true))

	b, err := json.Marshal(cmd)
	if err != nil {
		t.Fatal(err)
	}

	var decoded Command
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Name != "echo" {
		t.Errorf("Name = %q", decoded.Name)
	}
	if len(decoded.Options) != 1 {
		t.Fatalf("Options = %d", len(decoded.Options))
	}
	if decoded.Options[0].Name != "text" {
		t.Errorf("opt name = %q", decoded.Options[0].Name)
	}
}
