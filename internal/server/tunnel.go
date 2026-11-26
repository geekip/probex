package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"probex/internal/protocol"
	"probex/internal/tunnel"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type streamResult struct {
	stream *tunnel.Stream
	err    error
}
type dataStream struct {
	purpose, ticket string
	expires         time.Time
	ctx             context.Context
	result          chan streamResult
}

func (s *Server) openStream(ctx context.Context, nodeID string, m protocol.Message) (*tunnel.Stream, error) {
	ctx, cancel := context.WithCancel(ctx)
	d := &dataStream{purpose: m.Purpose, ticket: protocol.RandomID(), expires: time.Now().Add(10 * time.Second), ctx: ctx, result: make(chan streamResult, 1)}
	id := protocol.RandomID()
	ticket := d.ticket
	timer := time.NewTimer(time.Until(d.expires))
	defer timer.Stop()
	s.mu.Lock()
	p := s.peers[nodeID]
	if p == nil || p.ctx.Err() != nil {
		s.mu.Unlock()
		cancel()
		return nil, errors.New("node offline")
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		cancel()
		return nil, err
	}
	p.streams[id] = d
	s.mu.Unlock()
	stopPeer := context.AfterFunc(p.ctx, cancel)
	cleanup := func() {
		cancel()
		stopPeer()
		s.mu.Lock()
		delete(p.streams, id)
		s.mu.Unlock()
		if p.ctx.Err() == nil {
			if err := send(p.ctx, p.conn, protocol.Message{Type: "stream_close", ID: id}); err != nil {
				p.conn.CloseNow()
			}
		}
	}
	m.Type, m.ID, m.Ticket = "stream_open", id, ticket
	if err := send(p.ctx, p.conn, m); err != nil {
		cleanup()
		return nil, err
	}
	var result streamResult
	select {
	case result = <-d.result:
	case <-ctx.Done():
		result.err = ctx.Err()
	case <-timer.C:
		result.err = errors.New("stream setup timed out")
	}
	if result.err != nil {
		cleanup()
		return nil, result.err
	}
	go func() {
		select {
		case <-ctx.Done():
			result.stream.Close()
		case <-result.stream.Done():
		}
		cleanup()
	}()
	return result.stream, nil
}

func (s *Server) streamError(p *peer, m protocol.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := p.streams[m.ID]
	// Cancellation can cross an in-flight result on the control connection.
	if d != nil && d.ticket != "" {
		d.ticket = ""
		d.result <- streamResult{err: fmt.Errorf("%s: %s", d.purpose, m.Error)}
	}
	return nil
}

func (s *Server) agentData(w http.ResponseWriter, r *http.Request) {
	nodeID := r.Header.Get("X-Probex-ID")
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !protocol.ValidNodeID(nodeID) {
		problem(w, 403, "invalid node credentials")
		return
	}
	s.serveAgentData(w, r, nodeID, key)
}

func (s *Server) serveAgentData(w http.ResponseWriter, r *http.Request, nodeID, key string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		problem(w, 503, "server stopping")
		return
	}
	s.agents.Add(1)
	s.mu.Unlock()
	defer s.agents.Done()
	if nodeID != protocol.LocalNodeID {
		n, err := s.store.node(nodeID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			s.databaseError(w, err)
			return
		}
		if err != nil || n.Admission != "approved" || !equal(n.KeyHash, hash(key)) {
			problem(w, 403, "invalid node credentials")
			return
		}
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(protocol.MaxMessage)
	first, stop := context.WithTimeout(r.Context(), 10*time.Second)
	var h tunnel.Hello
	err = wsjson.Read(first, conn, &h)
	stop()
	if err != nil {
		return
	}
	s.mu.Lock()
	p := s.peers[nodeID]
	var d *dataStream
	if p != nil {
		d = p.streams[h.ID]
	}
	valid := h.NodeID == nodeID && d != nil && d.ctx.Err() == nil && p.ctx.Err() == nil && h.Purpose == d.purpose && d.ticket != "" && equal(h.Ticket, d.ticket) && time.Now().Before(d.expires)
	if !valid {
		s.mu.Unlock()
		conn.Close(websocket.StatusPolicyViolation, "invalid or expired stream ticket")
		return
	}
	d.ticket = ""
	stream := tunnel.New(d.ctx, conn)
	d.result <- streamResult{stream: stream}
	s.mu.Unlock()
	select {
	case <-d.ctx.Done():
	case <-stream.Done():
	}
	stream.Close()
}
