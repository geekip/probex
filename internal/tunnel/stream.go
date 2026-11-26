// Package tunnel carries bounded binary streams over independent WebSockets.
package tunnel

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"

	"github.com/coder/websocket"
)

const BlockSize = 32 << 10
const MaxFrame = 1 + 65535 + 8 // Also accommodates a complete UDP datagram and flow ID.
const (
	Data byte = iota
	FIN
)

type Hello struct {
	NodeID  string `json:"node_id"`
	ID      string `json:"id"`
	Purpose string `json:"purpose"`
	Ticket  string `json:"ticket"`
}

// Stream has one reader and serialized writers. FIN closes only the write half.
type Stream struct {
	ctx      context.Context
	conn     *websocket.Conn
	mu       sync.Mutex
	writeEOF bool
	readEOF  bool
	rest     []byte
	once     sync.Once
	done     chan struct{}
}

func New(ctx context.Context, conn *websocket.Conn) *Stream {
	conn.SetReadLimit(MaxFrame)
	return &Stream{ctx: ctx, conn: conn, done: make(chan struct{})}
}
func (s *Stream) Done() <-chan struct{} { return s.done }
func (s *Stream) Close() error {
	s.once.Do(func() { close(s.done); s.conn.CloseNow() })
	return nil
}
func (s *Stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(s.rest) == 0 {
		if s.readEOF {
			return 0, io.EOF
		}
		typ, b, err := s.conn.Read(s.ctx)
		if err != nil {
			s.Close()
			return 0, err
		}
		if typ != websocket.MessageBinary || len(b) == 0 {
			s.Close()
			return 0, errors.New("invalid tunnel frame")
		}
		switch b[0] {
		case Data:
			s.rest = b[1:]
		case FIN:
			if len(b) != 1 {
				s.Close()
				return 0, errors.New("invalid tunnel FIN")
			}
			s.readEOF = true
		default:
			s.Close()
			return 0, errors.New("unknown tunnel frame")
		}
	}
	n := copy(p, s.rest)
	s.rest = s.rest[n:]
	return n, nil
}
func (s *Stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeEOF {
		return 0, io.ErrClosedPipe
	}
	n := 0
	for len(p) > 0 {
		size := min(len(p), BlockSize)
		frame := make([]byte, 1+size)
		frame[0] = Data
		copy(frame[1:], p[:size])
		if err := s.conn.Write(s.ctx, websocket.MessageBinary, frame); err != nil {
			s.Close()
			return n, err
		}
		p = p[size:]
		n += size
	}
	return n, nil
}
func (s *Stream) CloseWrite() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeEOF {
		return nil
	}
	s.writeEOF = true
	err := s.conn.Write(s.ctx, websocket.MessageBinary, []byte{FIN})
	if err != nil {
		s.Close()
	}
	return err
}

// Bridge preserves request EOF so the target can finish its response.
func Bridge(ctx context.Context, stream *Stream, target *net.TCPConn) error {
	stop := context.AfterFunc(ctx, func() { stream.Close(); target.Close() })
	defer stop()
	defer stream.Close()
	defer target.Close()
	results := make(chan error, 2)
	go func() {
		_, err := io.Copy(target, stream)
		if err == nil {
			err = target.CloseWrite()
		}
		if err != nil {
			target.Close()
			stream.Close()
		}
		results <- err
	}()
	go func() {
		_, err := io.Copy(stream, target)
		if err == nil {
			err = stream.CloseWrite()
		}
		if err != nil {
			target.Close()
			stream.Close()
		}
		results <- err
	}()
	a, b := <-results, <-results
	return errors.Join(a, b)
}
