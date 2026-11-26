package agent

import (
	"context"
	"log"
	"net"
	"probex/internal/tunnel"
	"sync"
	"time"
)

type udpTarget struct {
	conn *net.UDPConn
	last time.Time
}

func proxyUDP(ctx context.Context, stream *tunnel.Stream, target string, idle time.Duration) error {
	var mu sync.Mutex
	flows := map[uint64]*udpTarget{}
	var wg sync.WaitGroup
	stop := context.AfterFunc(ctx, func() { stream.Close() })
	defer stop()
	done := make(chan struct{})
	defer func() {
		close(done)
		stream.Close()
		mu.Lock()
		for id, f := range flows {
			delete(flows, id)
			f.conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(min(idle/2, time.Second))
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				var expired []uint64
				mu.Lock()
				for id, f := range flows {
					if time.Since(f.last) >= idle {
						delete(flows, id)
						f.conn.Close()
						expired = append(expired, id)
					}
				}
				mu.Unlock()
				for _, id := range expired {
					if err := stream.WriteDatagram(tunnel.Datagram{Flow: id, Close: true}); err != nil {
						return
					}
				}
			}
		}
	}()
	closeFlow := func(id uint64, f *udpTarget, err error) {
		mu.Lock()
		active := flows[id] == f
		if active {
			delete(flows, id)
			f.conn.Close()
		}
		mu.Unlock()
		if active {
			log.Printf("UDP target flow %d: %v", id, err)
			stream.WriteDatagram(tunnel.Datagram{Flow: id, Close: true})
		}
	}
	for {
		d, err := stream.ReadDatagram()
		if err != nil {
			return err
		}
		mu.Lock()
		f := flows[d.Flow]
		if d.Close {
			if f != nil {
				delete(flows, d.Flow)
				f.conn.Close()
			}
			mu.Unlock()
			continue
		}
		mu.Unlock()
		if f == nil {
			dial, cancel := context.WithTimeout(ctx, 10*time.Second)
			conn, err := (&net.Dialer{}).DialContext(dial, "udp", target)
			cancel()
			if err != nil {
				log.Printf("UDP target flow %d: %v", d.Flow, err)
				if err := stream.WriteDatagram(tunnel.Datagram{Flow: d.Flow, Close: true}); err != nil {
					return err
				}
				continue
			}
			f = &udpTarget{conn: conn.(*net.UDPConn), last: time.Now()}
			mu.Lock()
			flows[d.Flow] = f
			mu.Unlock()
			wg.Add(1)
			go func(id uint64, f *udpTarget) {
				defer wg.Done()
				b := make([]byte, tunnel.MaxDatagram)
				for {
					n, err := f.conn.Read(b)
					if err != nil {
						closeFlow(id, f, err)
						return
					}
					mu.Lock()
					active := flows[id] == f
					if active {
						f.last = time.Now()
					}
					mu.Unlock()
					if !active {
						return
					}
					if err := stream.WriteDatagram(tunnel.Datagram{Flow: id, Data: b[:n]}); err != nil {
						return
					}
				}
			}(d.Flow, f)
		}
		mu.Lock()
		f.last = time.Now()
		active := flows[d.Flow] == f
		mu.Unlock()
		if active {
			f.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := f.conn.Write(d.Data); err != nil {
				closeFlow(d.Flow, f, err)
			}
		}
	}
}
