package tunnel

import (
	"bytes"
	"context"
	"github.com/coder/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func streamPair(t *testing.T) (*Stream, *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	ready := make(chan *Stream, 1)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := websocket.Accept(w, r, nil)
		if e != nil {
			return
		}
		s := New(ctx, c)
		ready <- s
		<-s.Done()
	}))
	c, _, e := websocket.Dial(ctx, strings.Replace(h.URL, "http", "ws", 1), nil)
	if e != nil {
		t.Fatal(e)
	}
	s := <-ready
	t.Cleanup(func() { cancel(); s.Close(); c.CloseNow(); h.Close() })
	return s, c
}
func TestBinaryStreamChunkingFIN(t *testing.T) {
	s, c := streamPair(t)
	payload := bytes.Repeat([]byte{0, 255, 17}, BlockSize)
	done := make(chan error, 1)
	go func() {
		_, err := s.Write(payload)
		if err == nil {
			err = s.CloseWrite()
		}
		done <- err
	}()
	peer := New(s.ctx, c)
	got, err := io.ReadAll(peer)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("binary payload length=%d error=%v", len(got), err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write([]byte("after FIN")); err != io.ErrClosedPipe {
		t.Fatal(err)
	}
	// Receiving FIN still permits writing a response.
	go func() {
		_, err := peer.Write([]byte("reply"))
		if err == nil {
			err = peer.CloseWrite()
		}
		done <- err
	}()
	got, err = io.ReadAll(s)
	if err != nil || string(got) != "reply" {
		t.Fatal(string(got), err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestInvalidFramesCloseStream(t *testing.T) {
	cases := []struct {
		name  string
		typ   websocket.MessageType
		frame []byte
	}{
		{"text", websocket.MessageText, []byte("x")},
		{"empty", websocket.MessageBinary, nil},
		{"unknown", websocket.MessageBinary, []byte{9}},
		{"fin-payload", websocket.MessageBinary, []byte{FIN, 1}},
		{"oversize", websocket.MessageBinary, make([]byte, MaxFrame+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, c := streamPair(t)
			go c.Write(s.ctx, tc.typ, tc.frame)
			if _, err := s.Read(make([]byte, 1)); err == nil {
				t.Fatal("invalid frame accepted")
			}
			select {
			case <-s.Done():
			default:
				t.Fatal("invalid stream left open")
			}
		})
	}
}
