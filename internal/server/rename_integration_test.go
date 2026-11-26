package server_test

import (
	"context"
	"net/http"
	"path/filepath"
	"probex/internal/agent"
	"probex/internal/protocol"
	"testing"
	"time"
)

func TestRenamePreservesIdentityAndEnrollment(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	original := f.node()
	command := f.wait(f.command(original.ID, "printf BEFORE_RENAME", 5))
	var renamed protocol.Node
	f.request("PATCH", "/api/nodes/"+original.ID, map[string]string{"name": "renamed-router"}, 200, &renamed)
	if renamed.ID != original.ID || renamed.Name != "renamed-router" || !renamed.Online || renamed.CreatedAt != original.CreatedAt {
		t.Fatal("rename changed identity or connection", renamed)
	}
	f.stopAgent()
	f.waitOffline(original.ID)
	f.startAgent() // Saved client still sends its original name.
	if got := f.node(); got.ID != original.ID || got.Name != renamed.Name {
		t.Fatal("saved identity could not reconnect with its original name", got)
	}
	result := f.wait(f.command(original.ID, "printf AFTER_RENAME", 5))
	if string(result.Stdout) != "AFTER_RENAME" {
		t.Fatal(result)
	}
	var history []protocol.Command
	f.request("GET", "/api/nodes/"+original.ID+"/commands", nil, 200, &history)
	if len(history) != 2 || history[1].ID != command.ID {
		t.Fatal("history lost", history)
	}
	f.request("POST", "/api/nodes", map[string]string{"name": renamed.Name}, 409, nil)
	f.request("PATCH", "/api/nodes/"+original.ID, map[string]string{"name": "bad name"}, 400, nil)
	f.request("PATCH", "/api/nodes/"+original.ID, map[string]string{"name": ""}, 400, nil)
	f.request("PATCH", "/api/nodes/"+original.ID, map[string]string{"name": renamed.Name, "extra": "bad"}, 400, nil)
	f.request("PATCH", "/api/nodes/"+original.ID, map[string]string{"name": renamed.Name}, 200, nil)
	req, err := http.NewRequest("PATCH", f.http.URL+"/api/nodes/"+original.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("unauthenticated rename", resp.StatusCode)
	}
	f.stopAgent()
	var created struct {
		Node protocol.Node `json:"node"`
		Key  string        `json:"key"`
	}
	f.request("POST", "/api/nodes", map[string]string{"name": "pending-before"}, 201, &created)
	f.request("PATCH", "/api/nodes/"+created.Node.ID, map[string]string{"name": renamed.Name}, 409, nil)
	f.request("GET", "/api/nodes/"+created.Node.ID, nil, 200, &renamed)
	if renamed.Name != "pending-before" || renamed.Admission != "invited" {
		t.Fatal("failed rename changed node", renamed)
	}
	f.request("PATCH", "/api/nodes/"+created.Node.ID, map[string]string{"name": "pending-after"}, 200, nil)
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.done = make(chan error, 1)
	state := filepath.Join(t.TempDir(), "renamed.json")
	go func() {
		f.done <- agent.Run(ctx, agent.Config{Server: f.http.URL, JoinKey: created.Key, State: state})
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		renamed = protocol.Node{}
		f.request("GET", "/api/nodes/"+created.Node.ID, nil, 200, &renamed)
		if renamed.Online {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("old join key failed after rename", renamed)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if renamed.Name != "pending-after" || renamed.Admission != "approved" {
		t.Fatal("enrollment overwrote display name", renamed)
	}
	f.stopAgent()
	f.request("DELETE", "/api/nodes/"+created.Node.ID, nil, 200, nil)
	f.request("PATCH", "/api/nodes/"+created.Node.ID, map[string]string{"name": "removed"}, 404, nil)
}
