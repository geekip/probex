package agent

import (
	"context"
	"fmt"
	"io"
	"probex/internal/protocol"
)

type console interface {
	io.ReadWriteCloser
	Resize(int, int) error
	Wait() (int, error)
}
type terminalSession struct {
	id      string
	console console
	cancel  context.CancelFunc
	input   chan protocol.Message
}

func dimensions(cols, rows int) bool { return cols > 0 && cols <= 1000 && rows > 0 && rows <= 1000 }
func (s *session) openTerminal(m protocol.Message) error {
	if m.ID == "" || !dimensions(m.Cols, m.Rows) {
		return fmt.Errorf("invalid terminal dimensions or ID")
	}
	s.mu.Lock()
	active := s.terminal != nil
	s.mu.Unlock()
	if active {
		return fmt.Errorf("server sent concurrent terminal")
	}
	pty, err := startConsole(m.Cols, m.Rows)
	if err != nil {
		return s.send(protocol.Message{Type: "term_exit", ID: m.ID, Error: err.Error()})
	}
	ctx, cancel := context.WithCancel(s.ctx)
	t := &terminalSession{id: m.ID, console: pty, cancel: cancel, input: make(chan protocol.Message, 8)}
	s.mu.Lock()
	s.terminal = t
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		stopClose := context.AfterFunc(ctx, func() { pty.Close() })
		defer stopClose()
		readDone := make(chan struct{})
		inputDone := make(chan struct{})
		go func() {
			defer close(readDone)
			data := make([]byte, protocol.ChunkSize)
			for {
				n, err := pty.Read(data)
				if n > 0 {
					if sendErr := s.send(protocol.Message{Type: "term_output", ID: m.ID, Data: data[:n]}); sendErr != nil {
						cancel()
						return
					}
				}
				if err != nil {
					if !consoleEOF(err) {
						cancel()
					}
					return
				}
			}
		}()
		go func() {
			defer close(inputDone)
			for {
				select {
				case <-ctx.Done():
					return
				case input := <-t.input:
					var err error
					if input.Type == "term_input" {
						_, err = pty.Write(input.Data)
					} else {
						err = pty.Resize(input.Cols, input.Rows)
					}
					if err != nil {
						cancel()
						return
					}
				}
			}
		}()
		if err := s.send(protocol.Message{Type: "term_started", ID: m.ID}); err != nil {
			cancel()
		}
		code, err := pty.Wait()
		wasClosed := ctx.Err() != nil
		cancel()
		pty.Close()
		<-readDone
		<-inputDone
		s.mu.Lock()
		if s.terminal == t {
			s.terminal = nil
		}
		s.mu.Unlock()
		result := protocol.Message{Type: "term_exit", ID: m.ID, ExitCode: &code}
		if wasClosed {
			result.Error = "terminal closed"
		} else if err != nil {
			result.Error = err.Error()
		}
		if err := s.send(result); err != nil {
			s.conn.CloseNow()
		}
	}()
	return nil
}
func (s *session) terminalControl(m protocol.Message) error {
	s.mu.Lock()
	t := s.terminal
	s.mu.Unlock()
	// Input may cross a terminal-exit notification on the two network directions.
	if t == nil || t.id != m.ID {
		return nil
	}
	if m.Type == "term_close" {
		t.cancel()
		return nil
	}
	if (m.Type == "term_resize" && !dimensions(m.Cols, m.Rows)) || (m.Type == "term_input" && len(m.Data) > protocol.ChunkSize) {
		return fmt.Errorf("terminal input outside limits")
	}
	select {
	case t.input <- m:
	default:
		t.cancel()
	}
	return nil
}
