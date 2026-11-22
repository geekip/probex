package server_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"probex/internal/agent"
	"probex/internal/protocol"
	"probex/internal/server"
	"testing"
	"time"
)

func TestEnrollmentTLSAndSavedIdentity(t *testing.T) {
	f := newFixture(t, false)
	f.http.Close()
	f.app.Close()
	var app *server.Server
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { app.Handler().ServeHTTP(w, r) }))
	t.Cleanup(tlsServer.Close)
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsServer.Certificate().Raw}))
	var err error
	app, err = server.New(f.store, server.Config{AdminToken: f.admin, Port: 17892, CA: ca})
	if err != nil {
		t.Fatal(err)
	}
	f.app = app
	f.http = httptest.NewServer(app.Handler())
	var created struct {
		Node protocol.Node `json:"node"`
		Key  string        `json:"key"`
	}
	f.request("POST", "/api/nodes", map[string]string{"name": "joined-node"}, 201, &created)
	join, err := protocol.DecodeEnrollment(created.Key)
	if err != nil || join.Name != "joined-node" || join.CA != ca || join.Port != 17892 {
		t.Fatal("bad join metadata", err)
	}
	f.request("POST", "/api/nodes", map[string]string{"name": "joined-node"}, 409, nil)
	// The shared registration entry is closed even when an otherwise valid node identity is provided.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err = agent.Run(ctx, agent.Config{Server: f.http.URL, Name: "legacy", State: filepath.Join(t.TempDir(), "legacy.json")})
	cancel()
	if err == nil {
		t.Fatal("shared registration remained enabled")
	}
	start := func(key string) {
		ctx, cancel := context.WithCancel(context.Background())
		f.cancel = cancel
		f.done = make(chan error, 1)
		go func() { f.done <- agent.Run(ctx, agent.Config{Server: tlsServer.URL, JoinKey: key, State: f.state}) }()
	}
	start(created.Key)
	node := f.node()
	if node.Admission != "approved" || node.ID != created.Node.ID {
		t.Fatal("wrong joined identity")
	}
	f.stopAgent()
	data, err := os.ReadFile(f.state)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(f.state); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("identity permissions", err)
	}
	// Model a lost welcome: the server has bound the key, but the client has no saved acknowledgement.
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	saved["enrolled"] = false
	withoutAck, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.state, withoutAck, 0600); err != nil {
		t.Fatal(err)
	}
	start("") // Name and trust material must come from the saved profile, with no join key or --ca.
	if f.node().ID != node.ID {
		t.Fatal("identity changed after restart")
	}
	result := f.wait(f.command(node.ID, "printf JOINED; exit 7", 5))
	if result.ExitCode == nil || *result.ExitCode != 7 {
		t.Fatal("joined node could not execute")
	}
	f.request("POST", "/api/nodes/"+node.ID+"/enrollment", map[string]string{}, 409, nil)
	f.stopAgent()
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	err = agent.Run(ctx, agent.Config{Server: tlsServer.URL, JoinKey: created.Key, State: filepath.Join(t.TempDir(), "stolen.json")})
	cancel()
	if err == nil {
		t.Fatal("consumed join key bound a second identity")
	}
	if len(data) == 0 {
		t.Fatal("identity was not saved")
	}
}
