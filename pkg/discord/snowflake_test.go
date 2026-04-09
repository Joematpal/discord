package discord

import (
	"encoding/json"
	"testing"
)

func TestSnowflake_String(t *testing.T) {
	s := Snowflake(123456789)
	if s.String() != "123456789" {
		t.Errorf("got %q", s.String())
	}
}

func TestSnowflake_MarshalJSON(t *testing.T) {
	s := Snowflake(999)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"999"` {
		t.Errorf("got %s", b)
	}
}

func TestSnowflake_UnmarshalJSON_String(t *testing.T) {
	var s Snowflake
	if err := json.Unmarshal([]byte(`"12345"`), &s); err != nil {
		t.Fatal(err)
	}
	if s != 12345 {
		t.Errorf("got %d", s)
	}
}

func TestSnowflake_UnmarshalJSON_Number(t *testing.T) {
	var s Snowflake
	if err := json.Unmarshal([]byte(`67890`), &s); err != nil {
		t.Fatal(err)
	}
	if s != 67890 {
		t.Errorf("got %d", s)
	}
}

func TestSnowflake_UnmarshalJSON_Empty(t *testing.T) {
	var s Snowflake
	if err := json.Unmarshal([]byte(`""`), &s); err != nil {
		t.Fatal(err)
	}
	if s != 0 {
		t.Errorf("got %d", s)
	}
}

func TestParseSnowflake(t *testing.T) {
	s, err := ParseSnowflake("42")
	if err != nil {
		t.Fatal(err)
	}
	if s != 42 {
		t.Errorf("got %d", s)
	}
}

func TestParseSnowflake_Invalid(t *testing.T) {
	_, err := ParseSnowflake("abc")
	if err == nil {
		t.Error("expected error")
	}
}

func TestSnowflake_JSONRoundtrip(t *testing.T) {
	type wrap struct {
		ID Snowflake `json:"id"`
	}
	orig := wrap{ID: 1234567890123456789}
	b, _ := json.Marshal(orig)
	var decoded wrap
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != orig.ID {
		t.Errorf("roundtrip: got %d, want %d", decoded.ID, orig.ID)
	}
}
