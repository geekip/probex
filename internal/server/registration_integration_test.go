package server_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"probex/internal/agent"
	"probex/internal/protocol"
	"probex/internal/server"
	"testing"
	"time"
)

func candidate(f *fixture, method, id, key, name string, want int) protocol.Node {
	f.t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name})
	path := "/register"
	if method == "GET" {
		path = "/registration"
	}
	req, err := http.NewRequest(method, f.http.URL+path, bytes.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Header.Set("X-Probex-ID", id)
	req.Header.Set("Authorization", "Bearer "+key)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		f.t.Fatalf("candidate %s got%d want%d", method, response.StatusCode, want)
	}
	var n protocol.Node
	if err = json.NewDecoder(response.Body).Decode(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestRegistrationApprovalGatesControlAndRejectionReleasesName(t *testing.T) {
	f := newFixture(t, true)
	key := protocol.RandomID()
	id := ""
	n := candidate(f, "POST", id, key, "request-node", 202)
	if n.Admission != "review" || n.Status != "review" || n.JoinMode != "request" || n.JoinedAt != nil {
		t.Fatal(n)
	}
	id = n.ID
	candidate(f, "POST", id, key, "request-node", 202)
	candidate(f, "POST", "", protocol.RandomID(), "request-node", 409)
	candidate(f, "GET", id, protocol.RandomID(), "", 403)
	f.request("POST", "/api/nodes/"+id+"/commands", map[string]any{"command": "id", "timeout_seconds": 5}, 409, nil)
	f.request("POST", "/api/nodes/"+id+"/reject", map[string]string{}, 200, &n)
	if n.Status != "rejected" {
		t.Fatal(n)
	}
	candidate(f, "GET", id, key, "", 403)
	candidate(f, "POST", id, key, "request-node", 403)
	var invitation struct {
		Node protocol.Node `json:"node"`
		Key  string        `json:"key"`
	}
	f.request("POST", "/api/nodes", map[string]string{"name": "request-node"}, 201, &invitation)
	f.request("POST", "/api/nodes/"+id+"/approve", map[string]string{}, 409, nil)
	f.request("POST", "/api/nodes/"+invitation.Node.ID+"/approve", map[string]string{}, 409, nil)
}

func TestProactiveAgentWaitsThenJoinsAndRejoins(t *testing.T) {
	f := newFixture(t, true)
	start := func(rejoin bool) {
		ctx, cancel := context.WithCancel(context.Background())
		f.cancel = cancel
		f.done = make(chan error, 1)
		go func() {
			f.done <- agent.Run(ctx, agent.Config{Server: f.http.URL, Name: "requested", State: f.state, Rejoin: rejoin})
		}()
	}
	waiting := func() protocol.Node {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var nodes []protocol.Node
			f.request("GET", "/api/nodes", nil, 200, &nodes)
			for _, n := range nodes {
				if n.Name == "requested" && n.Admission == "review" {
					return n
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("request missing")
		return protocol.Node{}
	}
	start(false)
	review := waiting()
	if review.Online {
		t.Fatal("review node connected")
	}
	f.request("POST", "/api/nodes/"+review.ID+"/approve", map[string]string{}, 200, nil)
	first := f.node()
	if first.ID != review.ID || first.JoinedAt == nil {
		t.Fatal(first)
	}
	result := f.wait(f.command(first.ID, "printf APPROVED", 5))
	if string(result.Stdout) != "APPROVED" {
		t.Fatal(result)
	}
	f.stopAgent()
	f.request("DELETE", "/api/nodes/"+first.ID, nil, 200, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err := agent.Run(ctx, agent.Config{Server: f.http.URL, State: f.state})
	cancel()
	if err == nil {
		t.Fatal("deleted identity reconnected")
	}
	start(true)
	second := waiting()
	if second.ID == first.ID {
		t.Fatal("rejoin reused revoked identity")
	}
	f.request("POST", "/api/nodes/"+second.ID+"/approve", map[string]string{}, 200, nil)
	if got := f.node(); got.ID != second.ID {
		t.Fatal(got)
	}
}

func TestDeletedNameRecreatedAndNewKeyReplacesSavedIdentity(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	first := f.node()
	f.stopAgent()
	f.request("DELETE", "/api/nodes/"+first.ID, nil, 200, nil)
	if err := os.Remove(f.state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err := agent.Run(ctx, agent.Config{Server: f.http.URL, JoinKey: f.join, State: f.state})
	cancel()
	if err == nil {
		t.Fatal("deleted key accepted after state removal")
	}
	var next struct {
		Node protocol.Node `json:"node"`
		Key  string        `json:"key"`
	}
	f.request("POST", "/api/nodes", map[string]string{"name": "test-node"}, 201, &next)
	if next.Node.ID == first.ID {
		t.Fatal("deleted record reused")
	}
	f.join = next.Key
	f.startAgent()
	if got := f.node(); got.ID != next.Node.ID {
		t.Fatal("new key did not replace saved identity", got)
	}
}

func TestTLSArgumentsAndTrustedProxyCookies(t *testing.T) {
	for _, args := range [][]string{{"listen", "--tls"}, {"listen", "--allow-http"}, {"listen", "--token", "old"}, {"listen", "--tls-cert", "missing.pem"}, {"list", "--token", "old"}} {
		if err := server.Main(args); err == nil {
			t.Fatal("old or incomplete flags accepted", args)
		}
	}
	f := newFixture(t, false)
	for _, c := range []struct {
		remote, forwarded string
		tls, secure       bool
	}{{"127.0.0.1:1", "", false, false}, {"127.0.0.1:1", "https", false, true}, {"198.51.100.1:1", "https", false, false}, {"198.51.100.1:1", "", true, true}} {
		body, _ := json.Marshal(map[string]string{"token": f.admin})
		req := httptest.NewRequest("POST", "http://probe.test/api/login", bytes.NewReader(body))
		req.RemoteAddr = c.remote
		req.Header.Set("X-Forwarded-Proto", c.forwarded)
		if c.tls {
			req.TLS = &tls.ConnectionState{}
		}
		recorder := httptest.NewRecorder()
		f.app.Handler().ServeHTTP(recorder, req)
		if recorder.Code != 200 || len(recorder.Result().Cookies()) != 1 || recorder.Result().Cookies()[0].Secure != c.secure {
			t.Fatal("cookie security", c, recorder.Header())
		}
	}
}

func TestNewServerInvitationWithSameNumericIDReplacesIdentity(t *testing.T) {
	first := newFixture(t, false)
	first.startAgent()
	old := first.node()
	first.stopAgent()
	before, err := os.ReadFile(first.state)
	if err != nil {
		t.Fatal(err)
	}
	second := newFixture(t, false)
	second.state = first.state
	second.startAgent()
	current := second.node()
	if old.ID != "1" || current.ID != "1" {
		t.Fatal(old.ID, current.ID)
	}
	after, err := os.ReadFile(second.state)
	if err != nil || bytes.Equal(before, after) {
		t.Fatal("new invitation retained wrong server credentials", err)
	}
}
