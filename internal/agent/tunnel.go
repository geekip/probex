package agent

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"probex/internal/protocol"
	"probex/internal/tunnel"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func (s *session) startStream(m protocol.Message) error {
	if m.ID == "" || m.Ticket == "" {
		return fmt.Errorf("invalid stream request")
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	if _, exists := s.streams[m.ID]; exists {
		s.mu.Unlock()
		cancel()
		return fmt.Errorf("duplicate stream ID")
	}
	s.streams[m.ID] = cancel
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer func() { s.mu.Lock(); delete(s.streams, m.ID); s.mu.Unlock() }()
		if err := s.runStream(ctx, m); err != nil && ctx.Err() == nil {
			if err := s.send(protocol.Message{Type: "stream_error", ID: m.ID, Error: err.Error()}); err != nil {
				s.conn.CloseNow()
			}
		}
	}()
	return nil
}
func (s *session) runStream(ctx context.Context, m protocol.Message) error {
	if m.Purpose != "tcp" && m.Purpose != "udp" && m.Purpose != "file" && m.Purpose != "terminal" {
		return fmt.Errorf("unsupported stream purpose %q", m.Purpose)
	}
	setup, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	var target net.Conn
	if m.Purpose == "tcp" {
		var err error
		target, err = (&net.Dialer{}).DialContext(setup, "tcp", m.Target)
		if err != nil {
			return err
		}
		defer target.Close()
	}
	var terminal *terminalSession
	running := false
	if m.Purpose == "terminal" {
		var err error
		terminal, err = s.prepareTerminal(ctx, m)
		if err != nil {
			return err
		}
		defer func() {
			if !running {
				drained := make(chan struct{})
				go func() { io.Copy(io.Discard, terminal.console); close(drained) }()
				terminal.console.Close()
				terminal.console.Wait()
				<-drained
				s.releaseTerminal(terminal)
			}
		}()
	}

	u, err := url.Parse(s.endpoint)
	if err != nil {
		return err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/data"
	conn, response, err := websocket.Dial(setup, u.String(), &websocket.DialOptions{HTTPClient: s.client, HTTPHeader: s.identity.headers()})
	if err != nil {
		if response != nil && response.Body != nil {
			defer response.Body.Close()
			return rejectedResponse(response)
		}
		return err
	}
	defer conn.CloseNow()
	if err := wsjson.Write(setup, conn, tunnel.Hello{NodeID: s.identity.ID, ID: m.ID, Purpose: m.Purpose, Ticket: m.Ticket}); err != nil {
		return err
	}
	stream := tunnel.New(ctx, conn)
	if m.Purpose == "terminal" {
		running = true
		return s.runTerminal(terminal, stream)
	}
	if m.Purpose == "file" {
		return s.proxyFile(ctx, stream)
	}
	if m.Purpose == "udp" {
		return proxyUDP(ctx, stream, m.Target, tunnel.UDPIdleTimeout)
	}
	return tunnel.Bridge(ctx, stream, target.(*net.TCPConn))
}
