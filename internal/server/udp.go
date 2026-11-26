package server

import (
	"log"
	"net"
	"net/netip"
	"probex/internal/protocol"
	"probex/internal/tunnel"
	"strconv"
	"sync"
	"time"
)

type udpVisitor struct {
	address netip.AddrPort
	last    time.Time
}
type udpForward struct {
	mu      sync.Mutex
	stream  *tunnel.Stream
	sources map[netip.AddrPort]uint64
	flows   map[uint64]udpVisitor
	next    uint64
	idle    time.Duration
}

func (u *udpForward) remove(id uint64) {
	if f, ok := u.flows[id]; ok {
		delete(u.sources, f.address)
		delete(u.flows, id)
	}
}
func (u *udpForward) reset(stream *tunnel.Stream) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.stream == stream {
		u.stream = nil
		clear(u.flows)
		clear(u.sources)
	}
	stream.Close()
}
func (s *Server) udpError(run *forwardRuntime, err error) {
	s.forwardsMu.Lock()
	defer s.forwardsMu.Unlock()
	run.lastError = ""
	if err != nil {
		run.lastError = err.Error()
	}
}
func (s *Server) startUDPForward(run *forwardRuntime) {
	u := &udpForward{sources: map[netip.AddrPort]uint64{}, flows: map[uint64]udpVisitor{}, idle: tunnel.UDPIdleTimeout}
	run.wg.Add(2)
	go func() { defer run.wg.Done(); s.serveUDP(run, u) }()
	go func() {
		defer run.wg.Done()
		tick := time.NewTicker(min(u.idle/2, time.Second))
		defer tick.Stop()
		for {
			select {
			case <-run.ctx.Done():
				return
			case <-tick.C:
				var expired []uint64
				u.mu.Lock()
				stream := u.stream
				for id, f := range u.flows {
					if time.Since(f.last) >= u.idle {
						u.remove(id)
						expired = append(expired, id)
					}
				}
				u.mu.Unlock()
				for _, id := range expired {
					if err := stream.WriteDatagram(tunnel.Datagram{Flow: id, Close: true}); err != nil {
						u.reset(stream)
						s.udpError(run, err)
						break
					}
				}
			}
		}
	}()
}
func (s *Server) serveUDP(run *forwardRuntime, u *udpForward) {
	b := make([]byte, tunnel.MaxDatagram)
	defer func() {
		u.mu.Lock()
		stream := u.stream
		u.mu.Unlock()
		if stream != nil {
			u.reset(stream)
		}
	}()
	for {
		n, source, err := run.udp.ReadFromUDPAddrPort(b)
		if err != nil {
			if run.ctx.Err() == nil {
				s.forwardsMu.Lock()
				run.listenError = err.Error()
				s.forwardsMu.Unlock()
			}
			return
		}
		u.mu.Lock()
		stream := u.stream
		u.mu.Unlock()
		if stream == nil {
			// Only this first packet is retained during setup. Kernel UDP buffers are finite.
			stream, err = s.openStream(run.ctx, run.config.NodeID, protocol.Message{Purpose: "udp", Target: net.JoinHostPort(run.config.TargetHost, strconv.Itoa(run.config.TargetPort))})
			if err != nil {
				s.udpError(run, err)
				continue
			}
			u.mu.Lock()
			u.stream = stream
			u.mu.Unlock()
			s.udpError(run, nil)
			run.wg.Add(1)
			go func(stream *tunnel.Stream) { defer run.wg.Done(); s.udpReplies(run, u, stream) }(stream)
		}
		u.mu.Lock()
		if u.stream != stream {
			u.mu.Unlock()
			continue
		}
		id := u.sources[source]
		if id == 0 {
			u.next++
			id = u.next
			u.sources[source] = id
		}
		u.flows[id] = udpVisitor{address: source, last: time.Now()}
		u.mu.Unlock()
		if err := stream.WriteDatagram(tunnel.Datagram{Flow: id, Data: b[:n]}); err != nil {
			s.udpError(run, err)
			u.reset(stream)
		}
	}
}
func (s *Server) udpReplies(run *forwardRuntime, u *udpForward, stream *tunnel.Stream) {
	defer u.reset(stream)
	for {
		d, err := stream.ReadDatagram()
		if err != nil {
			if run.ctx.Err() == nil {
				s.udpError(run, err)
			}
			return
		}
		u.mu.Lock()
		f, ok := u.flows[d.Flow]
		if d.Close {
			u.remove(d.Flow)
			u.mu.Unlock()
			continue
		}
		if ok {
			f.last = time.Now()
			u.flows[d.Flow] = f
		}
		u.mu.Unlock()
		if !ok {
			continue
		}
		run.udp.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := run.udp.WriteToUDPAddrPort(d.Data, f.address); err != nil {
			if run.ctx.Err() != nil {
				return
			}
			log.Printf("UDP visitor flow %d: %v", d.Flow, err)
			u.mu.Lock()
			u.remove(d.Flow)
			u.mu.Unlock()
			if err := stream.WriteDatagram(tunnel.Datagram{Flow: d.Flow, Close: true}); err != nil {
				return
			}
		}
	}
}
