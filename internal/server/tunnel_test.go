package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"probex/internal/agent"
	"probex/internal/protocol"
	"probex/internal/tunnel"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func tunnelFixture(t *testing.T) (*Server, string, string, string) {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	key := protocol.RandomID()
	n := nodeRow{Node: protocol.Node{Name: "stream-node", Admission: "approved", JoinMode: "request"}, KeyHash: hash(key)}
	if err := store.insertNode(&n); err != nil {
		t.Fatal(err)
	}
	s, err := New(store, Config{AdminToken: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(s.Handler())
	t.Cleanup(func() { s.Close(); h.Close(); store.Close() })
	return s, h.URL, n.ID, key
}
func controlPeer(t *testing.T, s *Server, base, id, key string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(base, "http", "ws", 1)+"/agent", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + key}, "X-Probex-ID": []string{id}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	if err := wsjson.Write(ctx, conn, protocol.Message{Type: "hello", Version: protocol.WireVersion, Name: "stream-node", Info: &protocol.Info{}}); err != nil {
		t.Fatal(err)
	}
	var m protocol.Message
	if err := wsjson.Read(ctx, conn, &m); err != nil || m.Type != "welcome" {
		t.Fatal(m, err)
	}
	return conn
}
func dataPeer(t *testing.T, base, id, key string, h tunnel.Hello) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(base, "http", "ws", 1)+"/agent/data", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + key}, "X-Probex-ID": []string{id}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	if err := wsjson.Write(ctx, c, h); err != nil {
		t.Fatal(err)
	}
	return c
}
func TestTunnelTicketBindingReplayExpiryAndCancel(t *testing.T) {
	s, base, id, key := tunnelFixture(t)
	control := controlPeer(t, s, base, id, key)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := make(chan streamResult, 1)
	go func() {
		c, e := s.openStream(ctx, id, protocol.Message{Purpose: "tcp", Target: "127.0.0.1:1"})
		result <- streamResult{c, e}
	}()
	var m protocol.Message
	if err := wsjson.Read(ctx, control, &m); err != nil {
		t.Fatal(err)
	}
	good := tunnel.Hello{NodeID: id, ID: m.ID, Purpose: m.Purpose, Ticket: m.Ticket}
	reject := func(h tunnel.Hello) {
		t.Helper()
		c := dataPeer(t, base, id, key, h)
		_, _, e := c.Read(ctx)
		if websocket.CloseStatus(e) != websocket.StatusPolicyViolation {
			t.Fatalf("ticket accepted: %v", e)
		}
	}
	wrong := good
	wrong.NodeID = "999"
	reject(wrong)
	wrong = good
	wrong.Purpose = "file"
	reject(wrong)
	wrong = good
	wrong.Ticket = "wrong"
	reject(wrong)
	s.mu.Lock()
	d := s.peers[id].streams[m.ID]
	d.expires = time.Now().Add(-time.Second)
	s.mu.Unlock()
	reject(good)
	s.mu.Lock()
	d.expires = time.Now().Add(time.Second)
	s.mu.Unlock()
	c := dataPeer(t, base, id, key, good)
	r := <-result
	if r.err != nil {
		t.Fatal(r.err)
	}
	reject(good)
	cancel()
	_, _, err := c.Read(context.Background())
	if err == nil {
		t.Fatal("cancel retained connection")
	}
	r.stream.Close()
}
func TestTunnelPeerReplacementClosesStreams(t *testing.T) {
	s, base, id, key := tunnelFixture(t)
	control := controlPeer(t, s, base, id, key)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := make(chan streamResult, 1)
	go func() {
		c, e := s.openStream(ctx, id, protocol.Message{Purpose: "tcp", Target: "unused"})
		result <- streamResult{c, e}
	}()
	var m protocol.Message
	if err := wsjson.Read(ctx, control, &m); err != nil {
		t.Fatal(err)
	}
	c := dataPeer(t, base, id, key, tunnel.Hello{NodeID: id, ID: m.ID, Purpose: m.Purpose, Ticket: m.Ticket})
	r := <-result
	if r.err != nil {
		t.Fatal(r.err)
	}
	// Keep reading the replaced control socket so its close handshake completes.
	go control.Read(ctx)
	controlPeer(t, s, base, id, key)
	select {
	case <-r.stream.Done():
	case <-ctx.Done():
		t.Fatal("old stream retained")
	}
	_, _, err := c.Read(ctx)
	if err == nil {
		t.Fatal("old data connection retained")
	}
}
func TestTunnelAgentHalfCloseConcurrentAndSlowStream(t *testing.T) {
	s, base, id, key := tunnelFixture(t)
	state := filepath.Join(t.TempDir(), "identity")
	data, _ := json.Marshal(map[string]any{"id": id, "key": key, "name": "stream-node", "join_mode": "request", "enrolled": true})
	if err := os.WriteFile(state, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx, agent.Config{Server: base, Name: "stream-node", State: state}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	for {
		s.mu.Lock()
		p := s.peers[id]
		s.mu.Unlock()
		if p != nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("agent not online")
		case <-time.After(10 * time.Millisecond):
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var servers sync.WaitGroup
	servers.Add(1)
	go func() {
		defer servers.Done()
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			servers.Add(1)
			go func() {
				defer servers.Done()
				defer c.Close()
				request, e := io.ReadAll(c)
				if e == nil {
					io.Copy(c, bytes.NewReader(bytes.Repeat(request, 10000)))
				}
			}()
		}
	}()
	// One connection deliberately has an unread response while the others finish.
	slow, err := s.openStream(ctx, id, protocol.Message{Purpose: "tcp", Target: listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := slow.Write(bytes.Repeat([]byte("s"), 10000)); err != nil {
		t.Fatal(err)
	}
	if err := slow.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var clients sync.WaitGroup
	for i := 0; i < 4; i++ {
		clients.Add(1)
		go func(i int) {
			defer clients.Done()
			c, e := s.openStream(ctx, id, protocol.Message{Purpose: "tcp", Target: listener.Addr().String()})
			if e != nil {
				t.Error(e)
				return
			}
			defer c.Close()
			request := []byte{byte(i), 0, 255}
			if _, e = c.Write(request); e != nil {
				t.Error(e)
				return
			}
			if e = c.CloseWrite(); e != nil {
				t.Error(e)
				return
			}
			response, e := io.ReadAll(c)
			if e != nil || !bytes.Equal(response, bytes.Repeat(request, 10000)) {
				t.Errorf("stream %d response length=%d err=%v", i, len(response), e)
			}
		}(i)
	}
	clients.Wait()
	s.mu.Lock()
	p := s.peers[id]
	s.mu.Unlock()
	ping, pingCancel := context.WithTimeout(ctx, time.Second)
	defer pingCancel()
	if err := p.conn.Ping(ping); err != nil {
		t.Fatal("data starved control", err)
	}
	slow.Close()
	cancel()
	listener.Close()
	servers.Wait()
}

func TestTunnelNodeDeleteClosesData(t *testing.T) {
	s, base, id, key := tunnelFixture(t)
	control := controlPeer(t, s, base, id, key)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan streamResult, 1)
	go func() {
		c, e := s.openStream(ctx, id, protocol.Message{Purpose: "tcp", Target: "unused"})
		result <- streamResult{c, e}
	}()
	var m protocol.Message
	if err := wsjson.Read(ctx, control, &m); err != nil {
		t.Fatal(err)
	}
	c := dataPeer(t, base, id, key, tunnel.Hello{NodeID: id, ID: m.ID, Purpose: m.Purpose, Ticket: m.Ticket})
	r := <-result
	if r.err != nil {
		t.Fatal(r.err)
	}
	go control.Read(ctx)
	req, _ := http.NewRequestWithContext(ctx, "DELETE", base+"/api/nodes/"+id, nil)
	req.Header.Set("Authorization", "Bearer admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	select {
	case <-r.stream.Done():
	case <-ctx.Done():
		t.Fatal("deleted node retained data stream")
	}
	if _, _, err := c.Read(ctx); err == nil {
		t.Fatal("deleted node retained socket")
	}
	req, _ = http.NewRequestWithContext(ctx, "GET", base+"/agent/data", nil)
	req.Header.Set("X-Probex-ID", id)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("revoked credentials accepted", resp.Status)
	}
}
func TestTunnelSetupErrorAndCancellation(t *testing.T) {
	s, base, id, key := tunnelFixture(t)
	control := controlPeer(t, s, base, id, key)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := s.openStream(ctx, id, protocol.Message{Purpose: "tcp", Target: "unused"})
		result <- err
	}()
	var m protocol.Message
	if err := wsjson.Read(ctx, control, &m); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(ctx, control, protocol.Message{Type: "stream_error", ID: m.ID, Error: "connection refused"}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatal(err)
	}
	var closed protocol.Message
	if err := wsjson.Read(ctx, control, &closed); err != nil || closed.Type != "stream_close" || closed.ID != m.ID {
		t.Fatal(closed, err)
	}
	pending, stop := context.WithCancel(ctx)
	go func() {
		_, err := s.openStream(pending, id, protocol.Message{Purpose: "tcp", Target: "unused"})
		result <- err
	}()
	if err := wsjson.Read(ctx, control, &m); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := <-result; err == nil {
		t.Fatal("cancellation returned success")
	}
	if err := wsjson.Read(ctx, control, &closed); err != nil || closed.Type != "stream_close" || closed.ID != m.ID {
		t.Fatal(closed, err)
	}
	s.mu.Lock()
	count := len(s.peers[id].streams)
	s.mu.Unlock()
	if count != 0 {
		t.Fatal("pending tickets retained", count)
	}
}
func TestTunnelRejectsOldWireVersion(t *testing.T) {
	_, base, id, key := tunnelFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(base, "http", "ws", 1)+"/agent", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + key}, "X-Probex-ID": []string{id}}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err := wsjson.Write(ctx, c, protocol.Message{Type: "hello", Version: protocol.WireVersion - 1, Name: "stream-node", Info: &protocol.Info{}}); err != nil {
		t.Fatal(err)
	}
	_, _, err = c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation || !strings.Contains(err.Error(), "upgrade server and client together") {
		t.Fatal(err)
	}
}
