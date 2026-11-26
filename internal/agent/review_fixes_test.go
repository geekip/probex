package agent

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"probex/internal/protocol"
	"probex/internal/tunnel"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestAgentEntryNeverFollowsRedirects(t *testing.T) {
	var reached atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		w.WriteHeader(200)
	}))
	defer destination.Close()
	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer entry.Close()
	ctx := t.Context()
	id := identity{ID: "1", Key: "fixture"}
	client, err := protocol.HTTPClient("")
	if err != nil {
		t.Fatal(err)
	}
	ws := strings.Replace(entry.URL, "http", "ws", 1) + "/agent"
	s := session{client: client, endpoint: ws, ctx: ctx, identity: id}
	checks := map[string]func() error{
		"registration": func() error { return awaitApproval(ctx, Config{Server: entry.URL, Name: "sample"}, &id, client) },
		"control": func() error {
			_, err := connect(ctx, Config{EnrollmentToken: "fixture"}, id, ws, client)
			return err
		},
		"data": func() error { return s.runStream(ctx, protocol.Message{Purpose: "file"}) },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); err == nil || !strings.Contains(err.Error(), "redirect refused") || reached.Load() {
				t.Fatalf("unexpected redirect result: %v, reached destination: %v", err, reached.Load())
			}
		})
	}
}

func TestTerminalResizeBurstKeepsLatest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	term := &terminalSession{ctx: ctx, cancel: cancel, resize: make(chan protocol.Message, 1)}
	session := &session{terminals: map[string]*terminalSession{"term": term}}
	for col := 1; col <= 1000; col++ {
		if err := session.terminalControl(protocol.Message{Type: "term_resize", ID: "term", Cols: col, Rows: 24}); err != nil {
			t.Fatal(err)
		}
	}
	if ctx.Err() != nil {
		t.Fatal("resize burst canceled an active terminal")
	}
	select {
	case latest := <-term.resize:
		if latest.Cols != 1000 {
			t.Fatal(latest)
		}
	default:
		t.Fatal("missing last size")
	}
}

func TestUDPWriteFailureClosesOnlyItsFlow(t *testing.T) {
	target, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		buf := make([]byte, 128)
		for {
			n, addr, err := target.ReadFromUDP(buf)
			if err != nil {
				return
			}
			target.WriteToUDP(buf[:n], addr)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = proxyUDP(ctx, tunnel.New(ctx, conn), target.LocalAddr().String(), 10*time.Second)
	}))
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http", "ws", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	stream := tunnel.New(ctx, conn)
	defer func() { stream.Close(); <-done }()
	exchange := func(send, want tunnel.Datagram) {
		t.Helper()
		if err := stream.WriteDatagram(send); err != nil {
			t.Fatal(err)
		}
		got, err := stream.ReadDatagram()
		if err != nil || got.Flow != want.Flow || got.Close != want.Close || string(got.Data) != string(want.Data) {
			t.Fatalf("unexpected datagram for flow %d: %+v, %v", send.Flow, got, err)
		}
	}
	good := tunnel.Datagram{Flow: 1, Data: []byte("active visitor")}
	exchange(good, good)
	// The tunnel can carry this payload size, but a UDP socket cannot write it.
	exchange(tunnel.Datagram{Flow: 2, Data: make([]byte, tunnel.MaxDatagram)}, tunnel.Datagram{Flow: 2, Close: true})
	exchange(good, good)
}
