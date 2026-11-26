package server

import (
	"context"
	"io"
	"net/http"
	"probex/internal/protocol"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type terminalPeer struct {
	id     string
	cancel context.CancelFunc
	events chan protocol.Message
	exit   chan protocol.Message
}

func (t *terminalPeer) event(m protocol.Message) {
	select {
	case t.events <- m:
	default:
		t.cancel()
	}
}
func (s *Server) terminal(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Header.Get("Origin") != "" && !sameOrigin(r) {
		problem(w, 403, "origin rejected")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	t := &terminalPeer{id: protocol.RandomID(), cancel: cancel, events: make(chan protocol.Message, 16), exit: make(chan protocol.Message, 1)}
	sessionID, _ := ctx.Value(sessionKey{}).(string)
	s.mu.Lock()
	p := s.peers[nodeID]
	if s.closed || p == nil || p.ctx.Err() != nil || len(p.terminals) >= protocol.MaxTerminals {
		s.mu.Unlock()
		problem(w, 409, "node offline or terminal limit reached (8)")
		return
	}
	expiry := s.sessions[sessionID]
	if sessionID != "" && !time.Now().Before(expiry) {
		s.mu.Unlock()
		problem(w, 401, "login expired")
		return
	}
	p.terminals[t.id] = t
	s.agents.Add(1)
	s.mu.Unlock()
	defer s.agents.Done()
	defer func() { s.mu.Lock(); delete(p.terminals, t.id); s.mu.Unlock() }()
	stopPeer := context.AfterFunc(p.ctx, cancel)
	defer stopPeer()
	if sessionID != "" {
		var deadline context.CancelFunc
		ctx, deadline = context.WithDeadline(ctx, expiry)
		defer deadline()
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(protocol.MaxMessage)
	stopSocket := context.AfterFunc(ctx, func() { conn.CloseNow() })
	defer stopSocket()
	s.mu.Lock()
	if sessionID != "" {
		if _, ok := s.sessions[sessionID]; !ok {
			s.mu.Unlock()
			return
		}
		s.terminals[conn] = sessionID
	}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.terminals, conn); s.mu.Unlock() }()
	var workers sync.WaitGroup
	// Stop all writers before the handler returns, including a blocked browser.
	defer func() { cancel(); conn.CloseNow(); workers.Wait() }()
	workers.Add(1)
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-t.events:
				if err := send(ctx, conn, m); err != nil {
					cancel()
					return
				}
			case <-ticker.C:
				ping, done := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(ping)
				done()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	stream, err := s.openStream(ctx, nodeID, protocol.Message{Purpose: "terminal", Target: t.id, Cols: 80, Rows: 24, CWD: r.URL.Query().Get("cwd")})
	if err != nil {
		send(ctx, conn, protocol.Message{Type: "term_exit", ID: t.id, Error: err.Error()})
		return
	}
	defer stream.Close()
	workers.Add(1)
	go func() {
		defer workers.Done()
		data := make([]byte, protocol.ChunkSize)
		for {
			n, err := stream.Read(data)
			if n > 0 {
				if err := send(ctx, conn, protocol.Message{Type: "term_output", ID: t.id, Data: data[:n]}); err != nil {
					cancel()
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					cancel()
					return
				}
				break
			}
		}
		select {
		case result := <-t.exit:
			if err := stream.CloseWrite(); err != nil {
				cancel()
				return
			}
			if err := send(ctx, conn, result); err == nil {
				conn.Close(websocket.StatusNormalClosure, "shell exited")
			}
			cancel()
		case <-ctx.Done():
		}
	}()
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
			if _, err := stream.Write(m.Data); err != nil {
				return
			}
			continue
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
		if ctx.Err() != nil {
			return
		}
		if err := send(p.ctx, p.conn, m); err != nil {
			p.conn.CloseNow()
			return
		}
	}
}
func (s *Server) terminalMessage(p *peer, m protocol.Message) error {
	s.mu.Lock()
	t := p.terminals[m.ID]
	if t != nil && m.Type == "term_exit" {
		delete(p.terminals, m.ID)
	}
	s.mu.Unlock()
	// Results may cross cancellation or peer replacement in flight.
	if t == nil {
		return nil
	}
	if m.Type == "term_exit" {
		t.exit <- m
	} else {
		t.event(m)
	}
	return nil
}
