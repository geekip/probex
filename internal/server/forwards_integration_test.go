package server_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"probex/internal/agent"
	"probex/internal/protocol"
	"probex/internal/server"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func freeForwardAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.Addr().String()
	l.Close()
	return a
}
func forwardBody(id, listen, target string) map[string]any {
	host, port, _ := net.SplitHostPort(target)
	number, _ := strconv.Atoi(port)
	return map[string]any{"node_id": id, "protocol": "tcp", "listen_addr": listen, "target_host": host, "target_port": number}
}
func fetchForward(t *testing.T, address, want string) {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://" + address + "/forward")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || string(b) != want {
		t.Fatal(string(b), err)
	}
}
func TestForwardHTTPPersistenceConflictRenameDelete(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	n := f.node()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/waiting" {
			fmt.Fprint(w, "x")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, "through-agent")
	}))
	defer target.Close()
	addr := freeForwardAddress(t)
	var mapping server.Forward
	body := forwardBody(n.ID, addr, strings.TrimPrefix(target.URL, "http://"))
	f.request("POST", "/api/forwards", body, 201, &mapping)
	if mapping.Status != "ready" || mapping.ID == "" {
		t.Fatal(mapping)
	}
	fetchForward(t, addr, "through-agent")
	f.stopAgent()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var offline []server.Forward
		f.request("GET", "/api/forwards", nil, 200, &offline)
		if len(offline) == 1 && offline[0].Status == "node_offline" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("offline status not reported")
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.startAgent()
	f.node()
	fetchForward(t, addr, "through-agent")
	f.request("POST", "/api/forwards", body, 409, nil)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	var failed map[string]string
	f.request("POST", "/api/forwards", forwardBody(n.ID, occupied.Addr().String(), strings.TrimPrefix(target.URL, "http://")), 409, &failed)
	if failed["code"] != "forward_port_unavailable" || failed["error"] == "" {
		t.Fatal(failed)
	}
	f.request("PATCH", "/api/nodes/"+n.ID, map[string]string{"name": "renamed"}, 200, nil)
	var list []server.Forward
	f.request("GET", "/api/forwards?node_id="+n.ID, nil, 200, &list)
	if len(list) != 1 || list[0].NodeName != "renamed" || list[0].NodeID != n.ID {
		t.Fatal(list)
	}
	// Restart both the service and Agent using the same database and identity.
	f.stopAgent()
	f.app.Close()
	f.http.Close()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	f.store, err = server.OpenStore(filepath.Join(filepath.Dir(f.state), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	f.app, err = server.New(f.store, server.Config{AdminToken: f.admin})
	if err != nil {
		t.Fatal(err)
	}
	f.http = httptest.NewServer(f.app.Handler())
	f.request("GET", "/api/forwards", nil, 200, &list)
	if len(list) != 1 || list[0].ID != mapping.ID || list[0].Status != "node_offline" {
		t.Fatal(list)
	}
	f.startAgent()
	f.node()
	fetchForward(t, addr, "through-agent")
	// Keep a connection active while deleting the node.
	active, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	fmt.Fprint(active, "GET /waiting HTTP/1.1\r\nHost: target\r\n\r\n")
	active.SetReadDeadline(time.Now().Add(3 * time.Second))
	response, err := http.ReadResponse(bufio.NewReader(active), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.ReadFull(response.Body, make([]byte, 1)); err != nil {
		t.Fatal("forward did not become active", err)
	}
	f.request("DELETE", "/api/nodes/"+n.ID, nil, 200, nil)
	select {
	case err := <-f.done:
		if err == nil || !strings.Contains(err.Error(), "revoked") {
			t.Fatal("unexpected Agent exit", err)
		}
		f.cancel()
		f.cancel = nil
	case <-time.After(3 * time.Second):
		t.Fatal("deleted Agent did not exit")
	}
	active.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := response.Body.Read(make([]byte, 1)); err == nil {
		t.Fatal("active connection survived deletion")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("active connection was not closed", err)
	}
	f.request("GET", "/api/forwards", nil, 200, &list)
	if len(list) != 0 {
		t.Fatal("node mappings retained", list)
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal("listener retained", err)
	}
	l.Close()
}
func runForwardCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	capture, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	previous := os.Stdout
	os.Stdout = capture
	err = server.Main(append([]string{"forward"}, args...))
	os.Stdout = previous
	if _, e := capture.Seek(0, 0); e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(capture)
	if e != nil {
		t.Fatal(e)
	}
	return string(b), err
}
func TestForwardCLIBatchMultiNodeAndPrevalidation(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	first := f.node()
	var joined struct {
		Key string `json:"key"`
	}
	f.request("POST", "/api/nodes", map[string]string{"name": "second"}, 201, &joined)
	join, err := protocol.DecodeEnrollment(joined.Key)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx, agent.Config{Server: f.http.URL, JoinKey: joined.Key, State: filepath.Join(t.TempDir(), "second.json")})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var nodes []protocol.Node
		f.request("GET", "/api/nodes", nil, 200, &nodes)
		online := false
		for _, n := range nodes {
			if n.ID == join.NodeID && n.Online {
				online = true
			}
		}
		if online {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second node offline")
		}
		time.Sleep(10 * time.Millisecond)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "multi-node") }))
	defer target.Close()
	targetAddr := strings.TrimPrefix(target.URL, "http://")
	_, port, _ := net.SplitHostPort(targetAddr)
	a, b := freeForwardAddress(t), freeForwardAddress(t)
	flags := []string{"--server", f.http.URL, "--admin-token", f.admin}
	// A malformed last pair prevents every creation, including earlier valid pairs.
	if _, err := runForwardCLI(t, append(flags, a, first.Name+":"+port, b, "second:invalid")...); err == nil {
		t.Fatal("invalid batch accepted")
	}
	var list []server.Forward
	f.request("GET", "/api/forwards", nil, 200, &list)
	if len(list) != 0 {
		t.Fatal("invalid batch partially applied")
	}
	output, err := runForwardCLI(t, append(flags, a, first.Name+":"+port, b, join.NodeID+":"+targetAddr)...)
	if err != nil || !strings.Contains(output, "ready") {
		t.Fatal(output, err)
	}
	var clients sync.WaitGroup
	for _, address := range []string{a, b} {
		clients.Add(1)
		go func(address string) { defer clients.Done(); fetchForward(t, address, "multi-node") }(address)
	}
	clients.Wait()
	f.request("GET", "/api/forwards", nil, 200, &list)
	if len(list) != 2 {
		t.Fatal(list)
	}
	output, err = runForwardCLI(t, append([]string{"list"}, append(flags, "--client", "second")...)...)
	if err != nil || !strings.Contains(output, "second") || strings.Contains(output, first.Name) {
		t.Fatal(output, err)
	}
	// One duplicate fails, a later valid pair still succeeds and its ID is printed.
	c := freeForwardAddress(t)
	output, err = runForwardCLI(t, append(flags, a, first.Name+":"+port, c, "second:"+port)...)
	if err == nil || !strings.Contains(output, c) {
		t.Fatal("partial result missing", output, err)
	}
	fetchForward(t, c, "multi-node")
	f.request("GET", "/api/forwards", nil, 200, &list)
	if len(list) != 3 {
		t.Fatal(list)
	}
	output, err = runForwardCLI(t, append([]string{"remove"}, append(flags, list[0].ID, "999999", list[1].ID, list[2].ID)...)...)
	if err == nil || strings.Count(output, "removed") != 3 {
		t.Fatal(output, err)
	}
	f.request("GET", "/api/forwards", nil, 200, &list)
	if len(list) != 0 {
		t.Fatal(list)
	}
}
