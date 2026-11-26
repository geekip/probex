package server_test

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"probex/internal/server"
	"strings"
	"sync"
	"testing"
	"time"
)

func udpEcho(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		b := make([]byte, 65535)
		for {
			n, from, err := c.ReadFromUDP(b)
			if err != nil {
				return
			}
			response := b[:n]
			if bytes.HasPrefix(response, []byte("identify")) || bytes.Equal(response, []byte("same")) {
				response = []byte(from.String() + "|same")
			}
			if _, err := c.WriteToUDP(response, from); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	t.Cleanup(func() { c.Close(); <-done })
	return c
}
func udpClient(t *testing.T, address string) *net.UDPConn {
	t.Helper()
	a, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.DialUDP("udp", nil, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
func udpExchange(c *net.UDPConn, data []byte) ([]byte, error) {
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write(data); err != nil {
		return nil, err
	}
	b := make([]byte, 65535)
	n, err := c.Read(b)
	return b[:n], err
}
func TestUDPForwardSourcesEmptyLargeBurstAndReconnection(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	target := udpEcho(t)
	// TCP and UDP use the same numeric listening port and separate configurations.
	address := freeForwardAddress(t)
	tcpTarget := httptest.NewServer(nil)
	defer tcpTarget.Close()
	f.request("POST", "/api/forwards", forwardBody(node.ID, address, strings.TrimPrefix(tcpTarget.URL, "http://")), 201, nil)
	body := forwardBody(node.ID, address, target.LocalAddr().String())
	body["protocol"] = "udp"
	var mapping server.Forward
	f.request("POST", "/api/forwards", body, 201, &mapping)
	if mapping.Status != "ready" {
		t.Fatal(mapping)
	}
	f.request("POST", "/api/forwards", body, 409, nil)
	a, b := udpClient(t, address), udpClient(t, address)
	identityA, err := udpExchange(a, []byte("identify-a"))
	if err != nil {
		t.Fatal(err)
	}
	identityB, err := udpExchange(b, []byte("identify-b"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(identityA, identityB) {
		t.Fatal("sources share an Agent socket")
	}
	var wg sync.WaitGroup
	for _, pair := range []struct {
		conn     *net.UDPConn
		identity []byte
	}{{a, identityA}, {b, identityB}} {
		wg.Add(1)
		go func(c *net.UDPConn, identity []byte) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				got, err := udpExchange(c, []byte("same"))
				if err != nil || !bytes.Equal(got, identity) {
					t.Errorf("source response mixed: %q want %q err %v", got, identity, err)
					return
				}
			}
		}(pair.conn, pair.identity)
	}
	wg.Wait()
	for _, payload := range [][]byte{{}, bytes.Repeat([]byte{0, 255, 42}, 21835), bytes.Repeat([]byte{7}, 65507)} {
		got, err := udpExchange(a, payload)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("UDP payload length=%d got=%d err=%v", len(payload), len(got), err)
		}
	}
	a.SetDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 32; i++ {
		if _, err := a.Write([]byte(fmt.Sprintf("burst-%02d", i))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 32; i++ {
		buf := make([]byte, 30)
		n, err := a.Read(buf)
		if err != nil || string(buf[:n]) != fmt.Sprintf("burst-%02d", i) {
			t.Fatal(i, string(buf[:n]), err)
		}
	}
	// Dropping the data socket preserves the mapping and starts a fresh source session.
	// Node disconnect/reconnect exercises the same data close and cleanup path.
	f.stopAgent()
	f.waitOffline(node.ID)
	f.startAgent()
	f.node()
	got, err := udpExchange(a, []byte("identify-new"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got, identityA) {
		t.Fatal("old Agent source socket retained")
	}
	// Service restart keeps both protocol configurations and restores the UDP listener.
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
	f.startAgent()
	f.node()
	if got, err := udpExchange(b, []byte("after-restart")); err != nil || string(got) != "after-restart" {
		t.Fatal(string(got), err)
	}
	f.request("DELETE", "/api/forwards/"+mapping.ID, nil, 200, nil)
	listen, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Fatal("UDP listener retained", err)
	}
	listen.Close()
	var list []server.Forward
	f.request("GET", "/api/forwards", nil, 200, &list)
	if len(list) != 1 || list[0].Protocol != "tcp" {
		t.Fatal("deleting UDP affected TCP", list)
	}
}
func TestUDPForwardCLIAndPortConflict(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	n := f.node()
	target := udpEcho(t)
	address := freeForwardAddress(t)
	output, err := runForwardCLI(t, "--server", f.http.URL, "--admin-token", f.admin, "--protocol", "udp", address, n.Name+":"+target.LocalAddr().String())
	if err != nil || !strings.Contains(output, "udp") {
		t.Fatal(output, err)
	}
	c := udpClient(t, address)
	got, err := udpExchange(c, []byte("cli-udp"))
	if err != nil || string(got) != "cli-udp" {
		t.Fatal(string(got), err)
	}
	var failed map[string]string
	body := forwardBody(n.ID, target.LocalAddr().String(), target.LocalAddr().String())
	body["protocol"] = "udp"
	f.request("POST", "/api/forwards", body, 409, &failed)
	if failed["code"] != "forward_port_unavailable" {
		t.Fatal(failed)
	}
	f.request("DELETE", "/api/nodes/"+n.ID, nil, 200, nil)
	select {
	case err := <-f.done:
		if err == nil || !strings.Contains(err.Error(), "revoked") {
			t.Fatal(err)
		}
		f.cancel()
		f.cancel = nil
	case <-time.After(3 * time.Second):
		t.Fatal("Agent not revoked")
	}
	l, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Fatal("deleted node retained UDP listener", err)
	}
	l.Close()
}

type udpDataCapture struct {
	http.ResponseWriter
	opened chan net.Conn
}

func (w udpDataCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, b, e := w.ResponseWriter.(http.Hijacker).Hijack()
	if e == nil {
		w.opened <- c
	}
	return c, b, e
}
func TestUDPDataChannelFailureDoesNotDropControl(t *testing.T) {
	f := newFixture(t, false)
	f.http.Close()
	opened := make(chan net.Conn, 2)
	f.http = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent/data" {
			w = udpDataCapture{w, opened}
		}
		f.app.Handler().ServeHTTP(w, r)
	}))
	f.startAgent()
	node := f.node()
	target := udpEcho(t)
	address := freeForwardAddress(t)
	body := forwardBody(node.ID, address, target.LocalAddr().String())
	body["protocol"] = "udp"
	f.request("POST", "/api/forwards", body, 201, nil)
	c := udpClient(t, address)
	before, err := udpExchange(c, []byte("identify-before"))
	if err != nil {
		t.Fatal(err)
	}
	data := <-opened
	data.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var list []server.Forward
		f.request("GET", "/api/forwards", nil, 200, &list)
		if len(list) == 1 && list[0].Status == "target_error" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("data failure not observed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var nodes []struct {
		ID     string `json:"id"`
		Online bool   `json:"online"`
	}
	f.request("GET", "/api/nodes", nil, 200, &nodes)
	if len(nodes) != 1 || !nodes[0].Online {
		t.Fatal("control disconnected", nodes)
	}
	after, err := udpExchange(c, []byte("identify-after"))
	if err != nil || bytes.Equal(before, after) {
		t.Fatal("source not recreated", string(after), err)
	}
}
