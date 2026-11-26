package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"probex/internal/agent"
	"probex/internal/protocol"
	"probex/internal/server"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	t                  *testing.T
	store              *server.Store
	app                *server.Server
	http               *httptest.Server
	join, admin, state string
	cancel             context.CancelFunc
	done               chan error
}

func newFixture(t *testing.T, allowRegistration bool) *fixture {
	t.Helper()
	return newFixtureWithConfig(t, server.Config{AllowRegistration: allowRegistration})
}
func newFixtureWithConfig(t *testing.T, config server.Config) *fixture {
	t.Helper()
	dir := t.TempDir()
	store, err := server.OpenStore(filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, store: store, admin: protocol.RandomID(), state: filepath.Join(dir, "node.json")}
	config.AdminToken = f.admin
	f.app, err = server.New(store, config)
	if err != nil {
		t.Fatal(err)
	}
	f.http = httptest.NewServer(f.app.Handler())
	t.Cleanup(func() {
		f.stopAgent()
		f.app.Close()
		f.http.Close()
		if err := f.store.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}
func (f *fixture) startAgent() {
	if f.join == "" {
		var created struct {
			Key string `json:"key"`
		}
		f.request("POST", "/api/nodes", map[string]string{"name": "test-node"}, 201, &created)
		f.join = created.Key
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.done = make(chan error, 1)
	go func() {
		f.done <- agent.Run(ctx, agent.Config{Server: f.http.URL, JoinKey: f.join, State: f.state})
	}()
}
func (f *fixture) stopAgent() {
	if f.cancel != nil {
		f.cancel()
		select {
		case err := <-f.done:
			if err != nil {
				f.t.Error(err)
			}
		case <-time.After(5 * time.Second):
			f.t.Error("agent did not stop")
		}
		f.cancel = nil
	}
}
func (f *fixture) request(method, path string, body any, want int, result any) {
	f.t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, f.http.URL+path, bytes.NewReader(data))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.admin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		b, _ := io.ReadAll(resp.Body)
		f.t.Fatalf("%s %s: got %d, want %d: %s", method, path, resp.StatusCode, want, b)
	}
	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			f.t.Fatal(err)
		}
	}
}
func (f *fixture) node() protocol.Node {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var nodes []protocol.Node
		f.request("GET", "/api/nodes", nil, 200, &nodes)
		if len(nodes) == 1 && nodes[0].Online {
			return nodes[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatal("node did not connect")
	return protocol.Node{}
}
func (f *fixture) waitOffline(id string) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var node protocol.Node
		f.request("GET", "/api/nodes/"+id, nil, 200, &node)
		if !node.Online {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatal("server still reports the previous connection online")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func (f *fixture) command(id, text string, timeout int) protocol.Command {
	f.t.Helper()
	var c protocol.Command
	f.request("POST", "/api/nodes/"+id+"/commands", map[string]any{"command": text, "timeout_seconds": timeout}, 202, &c)
	return c
}
func (f *fixture) wait(c protocol.Command) protocol.Command {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		f.request("GET", "/api/commands/"+c.ID, nil, 200, &c)
		if protocol.Terminal(c.Status) {
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatal("command did not finish")
	return c
}

func TestAgentLifecycleAndCommands(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	n := f.node()
	if n.Info.Hostname == "" || n.Info.Arch == "" || n.Info.MemoryTotal == nil {
		t.Fatalf("missing device information: %+v", n.Info)
	}
	c := f.wait(f.command(n.ID, "printf hello; printf error >&2; exit 7", 5))
	if c.Status != "failed" || c.ExitCode == nil || *c.ExitCode != 7 || string(c.Stdout) != "hello" || string(c.Stderr) != "error" {
		t.Fatalf("bad command result: %+v", c)
	}
	c = f.wait(f.command(n.ID, "printf success", 5))
	if c.Status != "succeeded" || string(c.Stdout) != "success" {
		t.Fatalf("bad success: %+v", c)
	}
	c = f.command(n.ID, "sleep 30", 1)
	f.request("POST", "/api/nodes/"+n.ID+"/commands", map[string]any{"command": "echo duplicate"}, 409, nil)
	c = f.wait(c)
	if c.Status != "timed_out" {
		t.Fatalf("expected timeout: %+v", c)
	}
	c = f.wait(f.command(n.ID, "head -c 300000 /dev/zero", 5))
	if !c.Truncated || len(c.Stdout) != protocol.MaxOutput || c.Status != "succeeded" {
		t.Fatalf("bad truncation: status=%s, size=%d, flag=%v", c.Status, len(c.Stdout), c.Truncated)
	}
	f.stopAgent()
	f.waitOffline(n.ID)
	f.startAgent()
	again := f.node()
	if again.ID != n.ID {
		t.Fatal("identity changed on reconnect")
	}
	c = f.command(n.ID, "sleep 30", 30)
	f.stopAgent()
	c = f.wait(c)
	if c.Status != "unknown" {
		t.Fatalf("disconnect result=%s", c.Status)
	}
	var history []protocol.Command
	f.request("GET", "/api/nodes/"+n.ID+"/commands", nil, 200, &history)
	if len(history) != 5 {
		t.Fatalf("history length=%d", len(history))
	}
	f.startAgent()
	f.node()
	f.request("DELETE", "/api/nodes/"+n.ID, nil, 200, nil)
	select {
	case err := <-f.done:
		if err == nil {
			t.Fatal("revoked agent should report rejection")
		}
		f.cancel()
		f.cancel = nil
	case <-time.After(5 * time.Second):
		t.Fatal("revoked agent kept running")
	}
	f.request("GET", "/api/nodes/"+n.ID, nil, 404, nil)
}

func TestLoginAndOrigin(t *testing.T) {
	f := newFixture(t, false)
	resp, err := http.Get(f.http.URL + "/api/nodes")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("unauthenticated API allowed")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	post := func(path, origin, body string, want int) {
		t.Helper()
		req, err := http.NewRequest("POST", f.http.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("%s: %d instead of %d", path, resp.StatusCode, want)
		}
	}
	post("/api/login", f.http.URL, `{"token":"bad"}`, 401)
	post("/api/login", "https://evil.example", `{"token":"`+f.admin+`"}`, 403)
	post("/api/login", f.http.URL, `{"token":"`+f.admin+`"}`, 200)
	resp, err = client.Get(f.http.URL + "/api/session")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("session not established")
	}
	post("/api/logout", "https://evil.example", `{}`, 403)
	post("/api/logout", f.http.URL, `{}`, 200)
	resp, err = client.Get(f.http.URL + "/api/session")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("logout did not revoke session")
	}
}

func TestAgentTLSAndInvalidRegistration(t *testing.T) {
	f := newFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err := agent.Run(ctx, agent.Config{Server: f.http.URL, Name: "rejected", State: filepath.Join(t.TempDir(), "node.json")})
	cancel()
	if err == nil {
		t.Fatal("invalid registration should fail permanently before context timeout")
	}
	var nodes []protocol.Node
	f.request("GET", "/api/nodes", nil, 200, &nodes)
	if len(nodes) != 0 {
		t.Fatal("rejected node was registered")
	}
	tlsServer := httptest.NewTLSServer(f.app.Handler())
	defer tlsServer.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tlsServer.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	var created struct {
		Key string `json:"key"`
	}
	f.request("POST", "/api/nodes", map[string]string{"name": "tls-node"}, 201, &created)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx, agent.Config{Server: tlsServer.URL, JoinKey: created.Key, State: filepath.Join(t.TempDir(), "identity.json"), CA: ca})
	}()
	n := f.node()
	if n.Name != "tls-node" {
		t.Fatalf("wrong TLS node: %+v", n)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("TLS agent did not stop")
	}
}

func TestManagementCLI(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	f.node()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(f.admin+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	options := []string{"--server", f.http.URL, "--admin-token-file", tokenFile}
	run := func(action string, extra ...string) (string, error) {
		t.Helper()
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		original := os.Stdout
		os.Stdout = writer
		err = server.Main(append(append([]string{action}, options...), extra...))
		os.Stdout = original
		writer.Close()
		output, readErr := io.ReadAll(reader)
		reader.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		return string(output), err
	}
	out, err := run("list")
	if err != nil || !strings.Contains(out, "test-node\tonline") {
		t.Fatal("list CLI", out, err)
	}
	out, err = run("exec", "--client", "test-node", "--command", "printf cli-output; exit 7")
	var remoteExit *protocol.ExitError
	if out != "cli-output" || !errors.As(err, &remoteExit) || remoteExit.Code != 7 {
		t.Fatal("exec CLI", out, err)
	}
	out, err = run("remove", "--client", "test-node")
	if err != nil || !strings.Contains(out, "removed test-node") {
		t.Fatal("remove CLI", out, err)
	}
	select {
	case <-f.done:
		f.cancel()
		f.cancel = nil
	case <-time.After(3 * time.Second):
		t.Fatal("remove CLI did not disconnect agent")
	}
}

func TestClientReconnectsToRestartedServer(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	before := f.node()
	c := f.wait(f.command(before.ID, "printf persisted-before-restart", 5))
	address := f.http.Listener.Addr().String()
	f.app.Close()
	f.http.Close()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := server.OpenStore(filepath.Join(filepath.Dir(f.state), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	f.store = store
	f.app, err = server.New(store, server.Config{AdminToken: f.admin})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	f.http = httptest.NewUnstartedServer(f.app.Handler())
	f.http.Listener.Close()
	f.http.Listener = listener
	f.http.Start()
	after := f.node()
	if after.ID != before.ID {
		t.Fatal("automatic reconnect changed node identity")
	}
	var history protocol.Command
	f.request("GET", "/api/commands/"+c.ID, nil, 200, &history)
	if string(history.Stdout) != "persisted-before-restart" {
		t.Fatal("history lost across server restart")
	}
}

func TestLongCommandKeepsHeartbeatAndReports(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	c := f.command(node.ID, "sleep 35; printf long-complete", 40)
	deadline := time.Now().Add(40 * time.Second)
	reported := false
	for time.Now().Before(deadline) {
		var current protocol.Node
		f.request("GET", "/api/nodes/"+node.ID, nil, 200, &current)
		if current.Online && current.Info.CollectedAt.After(node.Info.CollectedAt.Add(20*time.Second)) {
			reported = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !reported {
		t.Fatal("device report blocked during long command")
	}
	c = f.wait(c)
	if c.Status != "succeeded" || string(c.Stdout) != "long-complete" {
		t.Fatalf("long command interrupted heartbeat: %+v", c)
	}
}
