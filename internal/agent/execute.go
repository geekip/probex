package agent

import (
	"context"
	"errors"
	"probex/internal/protocol"
	"sync"
	"time"
)

type output struct {
	mu        sync.Mutex
	session   *session
	id        string
	remaining int
	truncated bool
}
type streamWriter struct {
	o      *output
	stream string
}

func (w streamWriter) Write(data []byte) (int, error) {
	w.o.mu.Lock()
	defer w.o.mu.Unlock()
	size := len(data)
	allowed := size
	if allowed > w.o.remaining {
		allowed = w.o.remaining
		w.o.truncated = true
	}
	for offset := 0; offset < allowed; {
		end := offset + protocol.ChunkSize
		if end > allowed {
			end = allowed
		}
		if err := w.o.session.send(protocol.Message{Type: "output", ID: w.o.id, Stream: w.stream, Data: data[offset:end]}); err != nil {
			return 0, err
		}
		offset = end
	}
	w.o.remaining -= allowed
	return size, nil
}
func (s *session) execute(m protocol.Message) {
	ctx, cancel := context.WithTimeout(s.ctx, time.Duration(m.TimeoutSeconds)*time.Second)
	defer cancel()
	cmd := shellCommand(ctx, m.Command)
	out := &output{session: s, id: m.ID, remaining: protocol.MaxOutput}
	cmd.Stdout = streamWriter{out, "stdout"}
	cmd.Stderr = streamWriter{out, "stderr"}
	result := protocol.Message{Type: "result", ID: m.ID}
	if err := cmd.Start(); err != nil {
		result.Error = err.Error()
	} else {
		if err := s.send(protocol.Message{Type: "started", ID: m.ID}); err != nil {
			cancel()
		}
		err := cmd.Wait()
		code := cmd.ProcessState.ExitCode()
		result.ExitCode = &code
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.TimedOut = true
			result.Error = "command timed out"
		} else if ctx.Err() != nil {
			result.Error = "command cancelled"
		} else if err != nil {
			result.Error = err.Error()
		}
	}
	result.Truncated = out.truncated
	<-s.commandBusy
	if err := s.send(result); err != nil {
		s.conn.CloseNow()
	}
}
