package tunnel

import (
	"encoding/binary"
	"errors"
	"github.com/coder/websocket"
	"time"
)

const MaxDatagram = 65535
const (
	packetData  byte = 2
	packetClose byte = 3
)
const UDPIdleTimeout = 60 * time.Second

type Datagram struct {
	Flow  uint64
	Data  []byte
	Close bool
}

// Each WebSocket message carries exactly one UDP datagram or flow close.
func (s *Stream) WriteDatagram(d Datagram) error {
	if d.Flow == 0 || len(d.Data) > MaxDatagram || d.Close && len(d.Data) != 0 {
		return errors.New("invalid UDP datagram")
	}
	b := make([]byte, 9+len(d.Data))
	b[0] = packetData
	if d.Close {
		b[0] = packetClose
	}
	binary.BigEndian.PutUint64(b[1:9], d.Flow)
	copy(b[9:], d.Data)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.conn.Write(s.ctx, websocket.MessageBinary, b); err != nil {
		s.Close()
		return err
	}
	return nil
}
func (s *Stream) ReadDatagram() (Datagram, error) {
	typ, b, err := s.conn.Read(s.ctx)
	if err != nil {
		s.Close()
		return Datagram{}, err
	}
	if typ != websocket.MessageBinary || len(b) < 9 || (b[0] != packetData && b[0] != packetClose) || b[0] == packetClose && len(b) != 9 || len(b) > 9+MaxDatagram {
		s.Close()
		return Datagram{}, errors.New("invalid UDP tunnel frame")
	}
	d := Datagram{Flow: binary.BigEndian.Uint64(b[1:9]), Data: b[9:], Close: b[0] == packetClose}
	if d.Flow == 0 {
		s.Close()
		return Datagram{}, errors.New("invalid UDP flow ID")
	}
	return d, nil
}
