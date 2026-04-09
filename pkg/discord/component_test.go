package discord

import (
	"encoding/json"
	"testing"
)

func TestActionRow(t *testing.T) {
	row := ActionRow(
		Button(ButtonPrimary, "Click Me", "btn_click"),
		Button(ButtonDanger, "Delete", "btn_delete"),
	)
	if row.Type != ComponentActionRow {
		t.Errorf("Type = %d", row.Type)
	}
	if len(row.Components) != 2 {
		t.Fatalf("children = %d", len(row.Components))
	}
}

func TestButton(t *testing.T) {
	b := Button(ButtonSuccess, "OK", "btn_ok")
	if b.Type != ComponentButton {
		t.Errorf("Type = %d", b.Type)
	}
	if b.Label != "OK" {
		t.Errorf("Label = %q", b.Label)
	}
	if b.CustomID != "btn_ok" {
		t.Errorf("CustomID = %q", b.CustomID)
	}
}

func TestLinkButton(t *testing.T) {
	b := LinkButton("Visit", "https://example.com")
	if b.Type != ComponentButton {
		t.Errorf("Type = %d", b.Type)
	}
	if b.URL != "https://example.com" {
		t.Errorf("URL = %q", b.URL)
	}
	if b.CustomID != "" {
		t.Error("link button should have no custom_id")
	}
}

func TestStringSelect(t *testing.T) {
	s := StringSelect("color_select",
		SelectOption{Label: "Red", Value: "red"},
		SelectOption{Label: "Blue", Value: "blue"},
	)
	if s.Type != ComponentStringSelect {
		t.Errorf("Type = %d", s.Type)
	}
	if len(s.Options) != 2 {
		t.Fatalf("options = %d", len(s.Options))
	}
}

func TestUserSelect(t *testing.T) {
	s := UserSelect("user_pick")
	if s.Type != ComponentUserSelect {
		t.Errorf("Type = %d", s.Type)
	}
}

func TestRoleSelect(t *testing.T) {
	s := RoleSelect("role_pick")
	if s.Type != ComponentRoleSelect {
		t.Errorf("Type = %d", s.Type)
	}
}

func TestChannelSelect(t *testing.T) {
	s := ChannelSelect("ch_pick", ChannelTypeGuildText)
	if s.Type != ComponentChannelSelect {
		t.Errorf("Type = %d", s.Type)
	}
	if len(s.ChannelTypes) != 1 {
		t.Fatalf("types = %d", len(s.ChannelTypes))
	}
}

func TestTextInput(t *testing.T) {
	ti := TextInput(TextInputParagraph, "Description", "desc_field")
	if ti.Type != ComponentTextInput {
		t.Errorf("Type = %d", ti.Type)
	}
	if ti.Label != "Description" {
		t.Errorf("Label = %q", ti.Label)
	}
}

func TestComponent_JSON_Roundtrip(t *testing.T) {
	row := ActionRow(
		Button(ButtonPrimary, "Go", "btn_go"),
		StringSelect("sel", SelectOption{Label: "A", Value: "a"}),
	)

	b, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}

	var decoded Component
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Type != ComponentActionRow {
		t.Errorf("Type = %d", decoded.Type)
	}
	if len(decoded.Components) != 2 {
		t.Fatalf("children = %d", len(decoded.Components))
	}
}

func TestComponent_MessageWithComponents(t *testing.T) {
	// Build a realistic interaction response with components.
	resp := &InteractionResponse{
		Type: CallbackMessage,
		Data: &InteractionResponseData{
			Content: "Pick a color:",
			Components: []Component{
				ActionRow(
					Button(ButtonPrimary, "Red", "color_red"),
					Button(ButtonSecondary, "Blue", "color_blue"),
					Button(ButtonSuccess, "Green", "color_green"),
				),
				ActionRow(
					StringSelect("color_dropdown",
						SelectOption{Label: "Red", Value: "red", Description: "A warm color"},
						SelectOption{Label: "Blue", Value: "blue", Description: "A cool color"},
					),
				),
			},
		},
	}

	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}

	var decoded InteractionResponse
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Data.Components) != 2 {
		t.Fatalf("rows = %d", len(decoded.Data.Components))
	}
	if len(decoded.Data.Components[0].Components) != 3 {
		t.Errorf("buttons = %d", len(decoded.Data.Components[0].Components))
	}
}
