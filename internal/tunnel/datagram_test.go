package tunnel

import (
	"bytes"
	"github.com/coder/websocket"
	"testing"
)

func TestDatagramBoundaries(t *testing.T) {
	s, c := streamPair(t)
	other := New(s.ctx, c)
	for _, d := range []Datagram{{Flow: 1}, {Flow: 2, Data: bytes.Repeat([]byte{255}, MaxDatagram)}, {Flow: 1, Data: []byte("after-large")}, {Flow: 2, Close: true}} {
		done := make(chan error, 1)
		go func(d Datagram) { done <- other.WriteDatagram(d) }(d)
		got, err := s.ReadDatagram()
		if err != nil || got.Flow != d.Flow || got.Close != d.Close || !bytes.Equal(got.Data, d.Data) {
			t.Fatalf("flow=%d len=%d error=%v", got.Flow, len(got.Data), err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []Datagram{{}, {Flow: 1, Data: make([]byte, MaxDatagram+1)}, {Flow: 1, Data: []byte("x"), Close: true}} {
		if err := s.WriteDatagram(d); err == nil {
			t.Fatal("invalid outbound datagram accepted")
		}
	}
}
func TestDatagramRejectsInvalidFrames(t *testing.T) {
	for _, frame := range [][]byte{{packetData}, {packetData, 0, 0, 0, 0, 0, 0, 0, 0}, {packetClose, 0, 0, 0, 0, 0, 0, 0, 1, 1}, {Data, 0, 0, 0, 0, 0, 0, 0, 1}, make([]byte, MaxFrame+1)} {
		t.Run("invalid", func(t *testing.T) {
			s, c := streamPair(t)
			go c.Write(s.ctx, websocket.MessageBinary, frame)
			if _, err := s.ReadDatagram(); err == nil {
				t.Fatal("invalid datagram accepted")
			}
		})
	}
}
