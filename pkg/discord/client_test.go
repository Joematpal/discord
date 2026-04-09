package discord

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_CreateGlobalCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/applications/111/commands" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bot test-token" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}

		var cmd Command
		json.NewDecoder(r.Body).Decode(&cmd)
		cmd.ID = 999
		json.NewEncoder(w).Encode(cmd)
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{
		ApplicationID: 111,
		BotToken:      "test-token",
		APIBase:       srv.URL,
	})

	cmd, err := c.CreateGlobalCommand(context.Background(), *NewSlashCommand("ping", "Ping!"))
	if err != nil {
		t.Fatal(err)
	}
	if cmd.ID != 999 {
		t.Errorf("ID = %d", cmd.ID)
	}
	if cmd.Name != "ping" {
		t.Errorf("Name = %q", cmd.Name)
	}
}

func TestClient_ListGlobalCommands(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		json.NewEncoder(w).Encode([]Command{
			{ID: 1, Name: "ping"},
			{ID: 2, Name: "help"},
		})
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{ApplicationID: 111, BotToken: "tok", APIBase: srv.URL})
	cmds, err := c.ListGlobalCommands(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 {
		t.Fatalf("got %d commands", len(cmds))
	}
}

func TestClient_DeleteGlobalCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/applications/111/commands/999" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{ApplicationID: 111, BotToken: "tok", APIBase: srv.URL})
	if err := c.DeleteGlobalCommand(context.Background(), 999); err != nil {
		t.Fatal(err)
	}
}

func TestClient_BulkOverwriteGlobalCommands(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s", r.Method)
		}
		var cmds []Command
		json.NewDecoder(r.Body).Decode(&cmds)
		for i := range cmds {
			cmds[i].ID = Snowflake(i + 1)
		}
		json.NewEncoder(w).Encode(cmds)
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{ApplicationID: 111, BotToken: "tok", APIBase: srv.URL})
	cmds, err := c.BulkOverwriteGlobalCommands(context.Background(), []Command{
		*NewSlashCommand("ping", "Ping"),
		*NewSlashCommand("help", "Help"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 {
		t.Fatalf("got %d", len(cmds))
	}
	if cmds[0].ID != 1 || cmds[1].ID != 2 {
		t.Errorf("IDs: %d, %d", cmds[0].ID, cmds[1].ID)
	}
}

func TestClient_GuildCommands(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/applications/111/guilds/222/commands" {
			t.Errorf("path = %s", r.URL.Path)
		}
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode([]Command{{ID: 1, Name: "guild-ping"}})
		case http.MethodPost:
			var cmd Command
			json.NewDecoder(r.Body).Decode(&cmd)
			cmd.ID = 10
			json.NewEncoder(w).Encode(cmd)
		}
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{ApplicationID: 111, BotToken: "tok", APIBase: srv.URL})

	// List
	cmds, err := c.ListGuildCommands(context.Background(), 222)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 || cmds[0].Name != "guild-ping" {
		t.Errorf("list: %v", cmds)
	}

	// Create
	cmd, err := c.CreateGuildCommand(context.Background(), 222, *NewSlashCommand("test", "Test"))
	if err != nil {
		t.Fatal(err)
	}
	if cmd.ID != 10 {
		t.Errorf("ID = %d", cmd.ID)
	}
}

func TestClient_RespondToInteraction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/interactions/100/mytoken/callback" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{ApplicationID: 111, BotToken: "tok", APIBase: srv.URL})
	err := c.RespondToInteraction(context.Background(), 100, "mytoken", *MessageResponse("hi"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestClient_CreateFollowup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/webhooks/111/mytoken" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(Message{ID: 555, Content: "followup"})
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{ApplicationID: 111, BotToken: "tok", APIBase: srv.URL})
	msg, err := c.CreateFollowup(context.Background(), "mytoken", InteractionResponseData{Content: "followup"})
	if err != nil {
		t.Fatal(err)
	}
	if msg.ID != 555 {
		t.Errorf("ID = %d", msg.ID)
	}
}

func TestClient_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"Missing Access","code":50001}`))
	}))
	defer srv.Close()

	c := NewClient(ClientConfig{ApplicationID: 111, BotToken: "tok", APIBase: srv.URL})
	_, err := c.ListGlobalCommands(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Status != 403 {
		t.Errorf("Status = %d", apiErr.Status)
	}
}

// Verify interface compliance at compile time.
var (
	_ CommandService    = (*Client)(nil)
	_ InteractionClient = (*Client)(nil)
)
