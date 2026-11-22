package server

import (
	"context"
	"fmt"
	"net/http"
	"probex/internal/protocol"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type terminalPeer struct {
	id   string
	conn *websocket.Conn
}

func (s *Server) terminal(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Header.Get("Origin") != "" && !sameOrigin(r) {
		problem(w, 403, "origin rejected")
		return
	}
	s.mu.Lock()
	p := s.peers[nodeID]
	if p == nil || p.terminal != nil {
		s.mu.Unlock()
		problem(w, 409, "node offline or terminal busy")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.mu.Unlock()
		return
	}
	t := &terminalPeer{id: protocol.RandomID(), conn: conn}
	p.terminal = t
	sessionID, _ := r.Context().Value(sessionKey{}).(string)
	ctx := r.Context()
	cancel := func() {}
	if sessionID != "" {
		ctx, cancel = context.WithDeadline(ctx, s.sessions[sessionID])
		s.terminals[conn] = sessionID
	}
	s.mu.Unlock()
	defer cancel()
	defer conn.CloseNow()
	conn.SetReadLimit(protocol.MaxMessage)
	heartbeat, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeat.Done():
				return
			case <-ticker.C:
				ping, done := context.WithTimeout(heartbeat, 10*time.Second)
				err := conn.Ping(ping)
				done()
				if err != nil {
					conn.CloseNow()
					return
				}
			}
		}
	}()
	defer func() {
		s.mu.Lock()
		delete(s.terminals, conn)
		stillActive := p.terminal == t
		s.mu.Unlock()
		if stillActive {
			if err := send(context.Background(), p.conn, protocol.Message{Type: "term_close", ID: t.id}); err != nil {
				p.conn.CloseNow()
			}
		}
	}()
	if err := send(context.Background(), p.conn, protocol.Message{Type: "term_open", ID: t.id, Cols: 80, Rows: 24}); err != nil {
		p.conn.CloseNow()
		return
	}
	for {
		var m protocol.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			return
		}
		switch m.Type {
		case "input":
			if len(m.Data) > protocol.ChunkSize {
				conn.Close(websocket.StatusPolicyViolation, "input too large")
				return
			}
			m.Type = "term_input"
		case "resize":
			if m.Cols < 1 || m.Cols > 1000 || m.Rows < 1 || m.Rows > 1000 {
				conn.Close(websocket.StatusPolicyViolation, "invalid dimensions")
				return
			}
			m.Type = "term_resize"
		case "close":
			m.Type = "term_close"
		default:
			conn.Close(websocket.StatusPolicyViolation, "invalid terminal message")
			return
		}
		m.ID = t.id
		if err := send(context.Background(), p.conn, m); err != nil {
			p.conn.CloseNow()
			return
		}
	}
}
func (s *Server) terminalMessage(p *peer, m protocol.Message) error {
	s.mu.Lock()
	t := p.terminal
	s.mu.Unlock()
	if t == nil || t.id != m.ID {
		return fmt.Errorf("unexpected terminal result")
	}
	if m.Type == "term_started" {
		return nil
	}
	if m.Type == "term_output" {
		if err := send(context.Background(), t.conn, m); err != nil {
			t.conn.CloseNow()
		}
		return nil
	}
	s.mu.Lock()
	if p.terminal == t {
		p.terminal = nil
	}
	s.mu.Unlock()
	if err := send(context.Background(), t.conn, m); err != nil {
		t.conn.CloseNow()
	} else {
		t.conn.Close(websocket.StatusNormalClosure, "shell exited")
	}
	return nil
}
