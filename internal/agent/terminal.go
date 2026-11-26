package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"probex/internal/protocol"
	"probex/internal/tunnel"
)

type console interface {
	io.ReadWriteCloser
	Resize(int, int) error
	Stop() error
	Wait() (int, error)
}
type terminalSession struct {
	id      string
	console console
	ctx     context.Context
	cancel  context.CancelFunc
	resize  chan protocol.Message
}

func startConsole(cols, rows int, cwd string) (console, error) {
	if cwd == "" {
		var err error
		cwd, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("terminal user directory: %w", err)
		}
	}
	return startPlatformConsole(cols, rows, cwd)
}

func dimensions(cols, rows int) bool { return cols > 0 && cols <= 1000 && rows > 0 && rows <= 1000 }
func (s *session) prepareTerminal(ctx context.Context, m protocol.Message) (*terminalSession, error) {
	if m.Target == "" || !dimensions(m.Cols, m.Rows) {
		return nil, fmt.Errorf("invalid terminal dimensions or ID")
	}
	ctx, cancel := context.WithCancel(ctx)
	t := &terminalSession{id: m.Target, ctx: ctx, cancel: cancel, resize: make(chan protocol.Message, 1)}
	s.mu.Lock()
	if s.terminals[t.id] != nil || len(s.terminals) >= protocol.MaxTerminals {
		s.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("node terminal limit reached or duplicate ID")
	}
	s.terminals[t.id] = t
	s.mu.Unlock()
	var err error
	t.console, err = startConsole(m.Cols, m.Rows, m.CWD)
	if err != nil {
		s.releaseTerminal(t)
		return nil, err
	}
	return t, nil
}
func (s *session) releaseTerminal(t *terminalSession) {
	t.cancel()
	s.mu.Lock()
	delete(s.terminals, t.id)
	s.mu.Unlock()
}
func (s *session) runTerminal(t *terminalSession, stream *tunnel.Stream) error {
	pty := t.console
	outputDone := make(chan struct{})
	inputDone := make(chan struct{})
	resizeDone := make(chan struct{})
	if err := s.send(protocol.Message{Type: "term_started", ID: t.id}); err != nil {
		t.cancel()
	}
	go func() {
		defer close(outputDone)
		data := make([]byte, protocol.ChunkSize)
		for {
			n, err := pty.Read(data)
			if n > 0 {
				if _, err := stream.Write(data[:n]); err != nil {
					t.cancel()
					break
				}
			}
			if err != nil {
				if !consoleEOF(err) {
					t.cancel()
				}
				break
			}
		}
		if err := stream.CloseWrite(); err != nil {
			t.cancel()
		}
	}()
	go func() {
		defer close(inputDone)
		writable := true
		data := make([]byte, protocol.ChunkSize)
		for {
			n, err := stream.Read(data)
			if n > 0 && writable {
				if _, writeErr := pty.Write(data[:n]); writeErr != nil {
					writable = false
					t.cancel()
				}
			}
			if err != nil {
				t.cancel()
				return
			}
		}
	}()
	go func() {
		defer close(resizeDone)
		for {
			select {
			case <-t.ctx.Done():
				return
			case m := <-t.resize:
				if err := pty.Resize(m.Cols, m.Rows); err != nil {
					t.cancel()
					return
				}
			}
		}
	}()
	// Keep draining output while ClosePseudoConsole shuts down its clients.
	stopClose := context.AfterFunc(t.ctx, func() { <-resizeDone; pty.Stop() })
	code, err := pty.Wait()
	wasClosed := t.ctx.Err() != nil
	t.cancel()
	<-resizeDone
	stopErr := pty.Stop()
	stopClose()
	<-outputDone
	pty.Close()
	s.releaseTerminal(t)
	result := protocol.Message{Type: "term_exit", ID: t.id, ExitCode: &code}
	if wasClosed {
		result.Error = "terminal closed"
	} else if err != nil {
		result.Error = err.Error()
	} else if stopErr != nil {
		result.Error = stopErr.Error()
	}
	if err := s.send(result); err != nil {
		stream.Close()
		s.conn.CloseNow()
	}
	// The server acknowledges output FIN and exit by finishing the input half.
	<-inputDone
	return nil
}
func (s *session) terminalControl(m protocol.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.terminals[m.ID]
	// A close/resize may cross the shell exit on the opposite direction.
	if t == nil {
		return nil
	}
	if m.Type == "term_close" {
		t.cancel()
		return nil
	}
	if !dimensions(m.Cols, m.Rows) {
		return fmt.Errorf("terminal dimensions outside limits")
	}
	select {
	case t.resize <- m:
	default:
		select {
		case <-t.resize:
		default:
		}
		t.resize <- m
	}
	return nil
}
