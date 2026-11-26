package server

import (
	"fmt"
	"net/http"
	"probex/internal/protocol"
	"strings"
	"time"
)

func (s *Server) execute(w http.ResponseWriter, r *http.Request, nodeID string) {
	var body struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.TimeoutSeconds == 0 {
		body.TimeoutSeconds = 60
	}
	if strings.TrimSpace(body.Command) == "" || len(body.Command) > 16<<10 || body.TimeoutSeconds < 1 || body.TimeoutSeconds > 3600 {
		problem(w, 400, "command or timeout outside limits")
		return
	}
	c := protocol.Command{ID: protocol.RandomID(), NodeID: nodeID, Command: body.Command, TimeoutSeconds: body.TimeoutSeconds, Status: "queued", Stdout: []byte{}, Stderr: []byte{}, CreatedAt: time.Now().UTC()}
	s.mu.Lock()
	p := s.peers[nodeID]
	if p == nil || p.command != "" {
		s.mu.Unlock()
		problem(w, 409, "node offline or command busy")
		return
	}
	if err := s.store.insertCommand(c); err != nil {
		s.mu.Unlock()
		s.databaseError(w, err)
		return
	}
	p.command = c.ID
	s.mu.Unlock()
	if err := send(p.ctx, p.conn, protocol.Message{Type: "exec", ID: c.ID, Command: c.Command, TimeoutSeconds: c.TimeoutSeconds}); err != nil {
		if saved := s.unknown(c.ID, "delivery result unknown"); saved != nil {
			s.databaseError(w, saved)
			return
		}
		p.conn.CloseNow()
		problem(w, 409, "delivery result unknown; do not automatically repeat")
		return
	}
	writeJSON(w, 202, c)
}
func (s *Server) commandMessage(p *peer, m protocol.Message) error {
	if m.Type == "term_started" || m.Type == "term_exit" {
		return s.terminalMessage(p, m)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	active := p.command
	if active == "" || m.ID != active {
		return fmt.Errorf("unexpected command result")
	}
	if m.Type != "started" && m.Type != "output" && m.Type != "result" {
		return fmt.Errorf("unknown message type")
	}
	if m.Type == "output" && (m.Stream != "stdout" && m.Stream != "stderr") {
		return fmt.Errorf("invalid output stream")
	}
	err := s.store.updateCommand(m.ID, func(c *protocol.Command) {
		if protocol.Terminal(c.Status) {
			return
		}
		now := time.Now().UTC()
		switch m.Type {
		case "started":
			c.Status = "running"
			c.StartedAt = &now
		case "output":
			remaining := protocol.MaxOutput - len(c.Stdout) - len(c.Stderr)
			data := m.Data
			if len(data) > remaining {
				data = data[:remaining]
				c.Truncated = true
			}
			if m.Stream == "stdout" {
				c.Stdout = append(c.Stdout, data...)
			} else {
				c.Stderr = append(c.Stderr, data...)
			}
		case "result":
			c.ExitCode = m.ExitCode
			c.Error = m.Error
			c.Truncated = c.Truncated || m.Truncated
			c.FinishedAt = &now
			c.Status = "failed"
			if m.TimedOut {
				c.Status = "timed_out"
			} else if m.ExitCode != nil && *m.ExitCode == 0 && m.Error == "" {
				c.Status = "succeeded"
			}
		}
	})
	if err == nil && m.Type == "result" {
		if p.command == m.ID {
			p.command = ""
		}
	}
	return err
}
