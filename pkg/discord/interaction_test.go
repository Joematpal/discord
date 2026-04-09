package discord

import (
	"encoding/json"
	"testing"
)

func TestInteraction_UnmarshalPing(t *testing.T) {
	raw := `{"id":"1","application_id":"2","type":1,"token":"tok","version":1}`
	var i Interaction
	if err := json.Unmarshal([]byte(raw), &i); err != nil {
		t.Fatal(err)
	}
	if i.Type != InteractionTypePing {
		t.Errorf("Type = %d", i.Type)
	}
	if i.Token != "tok" {
		t.Errorf("Token = %q", i.Token)
	}
}

func TestInteraction_UnmarshalCommand(t *testing.T) {
	raw := `{
		"id":"100","application_id":"200","type":2,"token":"t",
		"version":1,"guild_id":"300","channel_id":"400",
		"data":{"id":"500","name":"ping","type":1},
		"member":{"user":{"id":"600","username":"joe"},"nick":"joey"}
	}`
	var i Interaction
	if err := json.Unmarshal([]byte(raw), &i); err != nil {
		t.Fatal(err)
	}
	if i.Type != InteractionTypeCommand {
		t.Errorf("Type = %d", i.Type)
	}
	if i.Data == nil || i.Data.Name != "ping" {
		t.Error("data.name should be ping")
	}
	if i.Member == nil || i.Member.Nick != "joey" {
		t.Error("member nick should be joey")
	}
	if i.Member.User == nil || i.Member.User.Username != "joe" {
		t.Error("member user should be joe")
	}
}

func TestInteraction_UnmarshalComponent(t *testing.T) {
	raw := `{
		"id":"1","application_id":"2","type":3,"token":"t","version":1,
		"data":{"custom_id":"btn_confirm","component_type":2}
	}`
	var i Interaction
	if err := json.Unmarshal([]byte(raw), &i); err != nil {
		t.Fatal(err)
	}
	if i.Type != InteractionTypeComponent {
		t.Errorf("Type = %d", i.Type)
	}
	if i.Data.CustomID != "btn_confirm" {
		t.Errorf("CustomID = %q", i.Data.CustomID)
	}
}

func TestOptionData_Values(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		o := OptionData{Value: "hello"}
		if o.StringValue() != "hello" {
			t.Errorf("got %q", o.StringValue())
		}
	})
	t.Run("int", func(t *testing.T) {
		o := OptionData{Value: float64(42)}
		if o.IntValue() != 42 {
			t.Errorf("got %d", o.IntValue())
		}
	})
	t.Run("float64", func(t *testing.T) {
		o := OptionData{Value: float64(3.14)}
		if o.Float64Value() != 3.14 {
			t.Errorf("got %f", o.Float64Value())
		}
	})
	t.Run("bool", func(t *testing.T) {
		o := OptionData{Value: true}
		if !o.BoolValue() {
			t.Error("expected true")
		}
	})
	t.Run("nil", func(t *testing.T) {
		o := OptionData{}
		if o.StringValue() != "" || o.IntValue() != 0 || o.Float64Value() != 0 || o.BoolValue() {
			t.Error("zero values incorrect")
		}
	})
}

func TestResponseBuilders(t *testing.T) {
	t.Run("Pong", func(t *testing.T) {
		r := Pong()
		if r.Type != CallbackPong {
			t.Errorf("Type = %d", r.Type)
		}
	})
	t.Run("Message", func(t *testing.T) {
		r := MessageResponse("hello")
		if r.Type != CallbackMessage {
			t.Errorf("Type = %d", r.Type)
		}
		if r.Data.Content != "hello" {
			t.Errorf("Content = %q", r.Data.Content)
		}
	})
	t.Run("Ephemeral", func(t *testing.T) {
		r := EphemeralResponse("secret")
		if r.Data.Flags&FlagEphemeral == 0 {
			t.Error("should have ephemeral flag")
		}
	})
	t.Run("Defer", func(t *testing.T) {
		r := DeferResponse()
		if r.Type != CallbackDeferredMessage {
			t.Errorf("Type = %d", r.Type)
		}
	})
	t.Run("Autocomplete", func(t *testing.T) {
		r := AutocompleteResponse(Choice("Red", "red"), Choice("Blue", "blue"))
		if r.Type != CallbackAutocompleteResult {
			t.Errorf("Type = %d", r.Type)
		}
		if len(r.Data.Choices) != 2 {
			t.Errorf("choices = %d", len(r.Data.Choices))
		}
	})
	t.Run("Modal", func(t *testing.T) {
		r := ModalResponse("my-modal", "My Form",
			ActionRow(TextInput(TextInputShort, "Name", "name_field")),
		)
		if r.Type != CallbackModal {
			t.Errorf("Type = %d", r.Type)
		}
		if r.Data.CustomID != "my-modal" {
			t.Errorf("CustomID = %q", r.Data.CustomID)
		}
		if r.Data.Title != "My Form" {
			t.Errorf("Title = %q", r.Data.Title)
		}
		if len(r.Data.Components) != 1 {
			t.Fatalf("components = %d", len(r.Data.Components))
		}
	})
}

func TestInteractionResponse_JSON(t *testing.T) {
	r := MessageResponse("pong!")
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var decoded InteractionResponse
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Data.Content != "pong!" {
		t.Errorf("Content = %q", decoded.Data.Content)
	}
}
