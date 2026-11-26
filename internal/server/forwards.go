package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"probex/internal/protocol"
	"probex/internal/tunnel"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Forward struct {
	ID         string    `json:"id"`
	NodeID     string    `json:"node_id"`
	NodeName   string    `json:"node_name"`
	Name       string    `json:"name"`
	Protocol   string    `json:"protocol"`
	ListenAddr string    `json:"listen_addr"`
	TargetHost string    `json:"target_host"`
	TargetPort int       `json:"target_port"`
	CreatedAt  time.Time `json:"created_at"`
	Status     string    `json:"status"`
	Error      string    `json:"error"`
}
type forwardRuntime struct {
	forwardSockets
	config      Forward
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	listenError string
	lastError   string
}
type forwardSockets struct {
	tcp *net.TCPListener
	udp *net.UDPConn
}

func validateForward(f *Forward) error {
	if f.Protocol == "" {
		f.Protocol = "tcp"
	}
	if f.Protocol != "tcp" && f.Protocol != "udp" && f.Protocol != "tcp+udp" {
		return fmt.Errorf("unsupported forwarding protocol %q", f.Protocol)
	}
	address, err := net.ResolveTCPAddr("tcp", f.ListenAddr)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if address.Port < 1 || address.Port > 65535 {
		return fmt.Errorf("listen port must be 1..65535")
	}
	f.ListenAddr = address.String()
	f.Name = strings.TrimSpace(f.Name)
	if f.Name == "" {
		f.Name = f.ListenAddr
	}
	if f.TargetHost == "" || strings.ContainsAny(f.TargetHost, "[]/ \t\r\n") || f.TargetPort < 1 || f.TargetPort > 65535 {
		return fmt.Errorf("invalid target host or port")
	}
	return nil
}
func (s *Store) forwards(includeLocal bool) ([]Forward, error) {
	rows, err := s.db.Query(`SELECT CAST(forwards.id AS TEXT),CAST(node_id AS TEXT),nodes.name,forwards.name,protocol,listen_addr,target_host,target_port,created_at FROM forwards JOIN nodes ON nodes.id=forwards.node_id WHERE node_id != 0 OR ? ORDER BY forwards.id`, includeLocal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Forward{}
	for rows.Next() {
		var f Forward
		var stamp string
		if err := rows.Scan(&f.ID, &f.NodeID, &f.NodeName, &f.Name, &f.Protocol, &f.ListenAddr, &f.TargetHost, &f.TargetPort, &stamp); err != nil {
			return nil, err
		}
		f.CreatedAt, err = time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	return result, rows.Err()
}
func (s *Store) insertForward(f *Forward) error {
	f.CreatedAt = time.Now().UTC()
	r, err := s.db.Exec(`INSERT INTO forwards(node_id,name,protocol,listen_addr,target_host,target_port,created_at) VALUES(?,?,?,?,?,?,?)`, f.NodeID, f.Name, f.Protocol, f.ListenAddr, f.TargetHost, f.TargetPort, f.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	id, err := r.LastInsertId()
	f.ID = strconv.FormatInt(id, 10)
	return err
}
func (s *Store) deleteForward(id string) error {
	r, err := s.db.Exec(`DELETE FROM forwards WHERE id=?`, id)
	if err != nil {
		return err
	}
	count, err := r.RowsAffected()
	if err == nil && count == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (s *Store) forward(id string) (Forward, error) {
	var f Forward
	var stamp string
	err := s.db.QueryRow(`SELECT CAST(forwards.id AS TEXT),CAST(node_id AS TEXT),nodes.name,forwards.name,protocol,listen_addr,target_host,target_port,created_at FROM forwards JOIN nodes ON nodes.id=forwards.node_id WHERE forwards.id=?`, id).Scan(&f.ID, &f.NodeID, &f.NodeName, &f.Name, &f.Protocol, &f.ListenAddr, &f.TargetHost, &f.TargetPort, &stamp)
	if err != nil {
		return f, err
	}
	f.CreatedAt, err = time.Parse(time.RFC3339Nano, stamp)
	return f, err
}
func (s *Store) updateForward(f Forward) error {
	_, err := s.db.Exec(`UPDATE forwards SET name=?,protocol=?,listen_addr=?,target_host=?,target_port=? WHERE id=?`, f.Name, f.Protocol, f.ListenAddr, f.TargetHost, f.TargetPort, f.ID)
	return err
}
func (s *Store) forwardConflict(f Forward) (bool, error) {
	var conflict bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM forwards WHERE id<>? AND listen_addr=? AND (protocol=? OR protocol='tcp+udp' OR ?='tcp+udp'))`, f.ID, f.ListenAddr, f.Protocol, f.Protocol).Scan(&conflict)
	return conflict, err
}

// Acquire the actual listeners before saving. Keep sockets already owned by
// the mapping when its listen address is unchanged.
func listenForward(f Forward, sockets forwardSockets) (forwardSockets, error) {
	if f.Protocol == "tcp" {
		sockets.udp = nil
	}
	if f.Protocol == "udp" {
		sockets.tcp = nil
	}
	var openedUDP *net.UDPConn
	if f.Protocol != "tcp" && sockets.udp == nil {
		addr, err := net.ResolveUDPAddr("udp", f.ListenAddr)
		if err != nil {
			return forwardSockets{}, err
		}
		openedUDP, err = net.ListenUDP("udp", addr)
		if err != nil {
			return forwardSockets{}, err
		}
		sockets.udp = openedUDP
	}
	if f.Protocol != "udp" && sockets.tcp == nil {
		listener, err := net.Listen("tcp", f.ListenAddr)
		if err != nil {
			if openedUDP != nil {
				openedUDP.Close()
			}
			return forwardSockets{}, err
		}
		sockets.tcp = listener.(*net.TCPListener)
	}
	return sockets, nil
}

func (s *Server) startForward(f Forward, sockets forwardSockets, bindErr error) {
	ctx, cancel := context.WithCancel(context.Background())
	run := &forwardRuntime{forwardSockets: sockets, config: f, ctx: ctx, cancel: cancel}
	s.forwards[f.ID] = run
	if bindErr != nil {
		run.listenError = bindErr.Error()
		log.Printf("forward %s (%q) %s %s: listen port unavailable: %v", f.ID, f.Name, f.Protocol, f.ListenAddr, bindErr)
		return
	}
	if run.udp != nil {
		s.startUDPForward(run)
	}
	if run.tcp == nil {
		return
	}
	run.wg.Add(1)
	go func() {
		defer run.wg.Done()
		for {
			conn, err := run.tcp.AcceptTCP()
			if err != nil {
				if ctx.Err() == nil {
					s.forwardsMu.Lock()
					run.listenError = err.Error()
					s.forwardsMu.Unlock()
					s.publishUpdate("forwards", run.config.NodeID, run.config.ID)
				}
				return
			}
			run.wg.Add(1)
			go func() { defer run.wg.Done(); s.forwardTCP(run, conn) }()
		}
	}()
}
func (s *Server) forwardTCP(run *forwardRuntime, conn *net.TCPConn) {
	defer conn.Close()
	stop := context.AfterFunc(run.ctx, func() { conn.Close() })
	defer stop()
	stream, err := s.openStream(run.ctx, run.config.NodeID, protocol.Message{Purpose: "tcp", Target: net.JoinHostPort(run.config.TargetHost, strconv.Itoa(run.config.TargetPort))})
	s.setForwardError(run, err)
	if err != nil {
		return
	}
	if err := tunnel.Bridge(run.ctx, stream, conn); err != nil && run.ctx.Err() == nil {
		log.Printf("forward %s: %v", run.config.ID, err)
	}
}
func (sockets forwardSockets) closeExcept(keep forwardSockets) {
	if sockets.udp != nil && sockets.udp != keep.udp {
		sockets.udp.Close()
	}
	if sockets.tcp != nil && sockets.tcp != keep.tcp {
		sockets.tcp.Close()
	}
}

func (r *forwardRuntime) stop(keep forwardSockets) {
	r.cancel()
	r.forwardSockets.closeExcept(keep)
	// Wake the old workers before handing their sockets to the new runtime.
	keepUDP := r.udp != nil && r.udp == keep.udp
	keepTCP := r.tcp != nil && r.tcp == keep.tcp
	if keepUDP {
		r.udp.SetReadDeadline(time.Now())
	}
	if keepTCP {
		r.tcp.SetDeadline(time.Now())
	}
	r.wg.Wait()
	if keepUDP {
		r.udp.SetReadDeadline(time.Time{})
	}
	if keepTCP {
		r.tcp.SetDeadline(time.Time{})
	}
}
func (s *Server) stopNodeForwards(nodeID string) {
	s.forwardsMu.Lock()
	var list []*forwardRuntime
	for id, r := range s.forwards {
		if nodeID == "" || r.config.NodeID == nodeID {
			list = append(list, r)
			delete(s.forwards, id)
		}
	}
	s.forwardsMu.Unlock()
	for _, r := range list {
		r.stop(forwardSockets{})
	}
}
func (s *Server) forwardStatus(f Forward) Forward {
	r := s.forwards[f.ID]
	f.Status = "ready"
	if r == nil {
		f.Status = "stopped"
		return f
	}
	f.Error = r.lastError
	s.mu.Lock()
	online := s.peers[f.NodeID] != nil
	s.mu.Unlock()
	if f.Error != "" {
		f.Status = "target_error"
	}
	if !online {
		f.Status = "node_offline"
		f.Error = "node offline"
	}
	if r.listenError != "" {
		f.Status = "listen_error"
		f.Error = r.listenError
	}
	return f
}
func (s *Server) forwardAPI(w http.ResponseWriter, r *http.Request, path string) {
	if path == "forwards" && r.Method == "GET" {
		s.forwardsMu.Lock()
		defer s.forwardsMu.Unlock()
		list, err := s.store.forwards(s.config.LocalNode)
		if err != nil {
			s.databaseError(w, err)
			return
		}
		result := []Forward{}
		node := r.URL.Query().Get("node_id")
		for _, f := range list {
			if node == "" || node == f.NodeID {
				result = append(result, s.forwardStatus(f))
			}
		}
		writeJSON(w, 200, result)
		return
	}
	if (path == "forwards" && r.Method == "POST") || (strings.HasPrefix(path, "forwards/") && r.Method == "PUT") {
		var body struct {
			NodeID     string `json:"node_id"`
			Name       string `json:"name"`
			Protocol   string `json:"protocol"`
			ListenAddr string `json:"listen_addr"`
			TargetHost string `json:"target_host"`
			TargetPort int    `json:"target_port"`
		}
		if !decode(w, r, &body) {
			return
		}
		f := Forward{Name: body.Name, NodeID: body.NodeID, Protocol: body.Protocol, ListenAddr: body.ListenAddr, TargetHost: body.TargetHost, TargetPort: body.TargetPort}
		if err := validateForward(&f); err != nil {
			problem(w, 400, err.Error())
			return
		}
		s.forwardOpsMu.Lock()
		defer s.forwardOpsMu.Unlock()
		s.forwardsMu.Lock()
		defer s.forwardsMu.Unlock()
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			problem(w, 503, "server stopping")
			return
		}
		if r.Method == "PUT" {
			f.ID = strings.TrimPrefix(path, "forwards/")
			if !protocol.ValidNodeID(f.ID) {
				problem(w, 400, "invalid forward ID")
				return
			}
			previous, err := s.store.forward(f.ID)
			if err != nil {
				s.lookupError(w, err)
				return
			}
			if body.NodeID != "" && body.NodeID != previous.NodeID {
				problem(w, 400, "cannot change forward node")
				return
			}
			f.NodeID, f.CreatedAt = previous.NodeID, previous.CreatedAt
		}
		if !s.nodeEnabled(f.NodeID) {
			problem(w, http.StatusNotFound, "node not found")
			return
		}
		n, err := s.store.node(f.NodeID)
		if err != nil {
			s.lookupError(w, err)
			return
		}
		if n.Admission != "approved" {
			problem(w, 409, "node is not approved")
			return
		}
		f.NodeID, f.NodeName = n.ID, n.Name
		conflict, err := s.store.forwardConflict(f)
		if err != nil {
			s.databaseError(w, err)
			return
		}
		if conflict {
			forwardPortError(w, "forward listen address already configured")
			return
		}
		previous := s.forwards[f.ID]
		var keep forwardSockets
		if previous != nil && previous.config.ListenAddr == f.ListenAddr && previous.listenError == "" {
			keep = previous.forwardSockets
		}
		sockets, err := listenForward(f, keep)
		if err != nil {
			forwardPortError(w, err.Error())
			return
		}
		status := http.StatusCreated
		if r.Method == "PUT" {
			err = s.store.updateForward(f)
			status = http.StatusOK
		} else {
			err = s.store.insertForward(&f)
		}
		if err != nil {
			sockets.closeExcept(keep)
			if isUniqueConstraint(err) {
				forwardPortError(w, "forward listen address already configured")
			} else {
				s.databaseError(w, err)
			}
			return
		}
		if previous != nil && previous.listenError == "" && previous.config.Protocol == f.Protocol && previous.config.ListenAddr == f.ListenAddr && previous.config.TargetHost == f.TargetHost && previous.config.TargetPort == f.TargetPort {
			s.publishUpdate("forwards", f.NodeID, f.ID)
			writeJSON(w, status, s.forwardStatus(f))
			return
		}
		delete(s.forwards, f.ID)
		s.forwardsMu.Unlock()
		if previous != nil {
			previous.stop(sockets)
		}
		s.forwardsMu.Lock()
		s.startForward(f, sockets, nil)
		s.publishUpdate("forwards", f.NodeID, f.ID)
		writeJSON(w, status, s.forwardStatus(f))
		return
	}
	if strings.HasPrefix(path, "forwards/") && r.Method == "DELETE" {
		id := strings.TrimPrefix(path, "forwards/")
		if !protocol.ValidNodeID(id) {
			problem(w, 400, "invalid forward ID")
			return
		}
		s.forwardOpsMu.Lock()
		defer s.forwardOpsMu.Unlock()
		s.forwardsMu.Lock()
		err := s.store.deleteForward(id)
		run := s.forwards[id]
		if err == nil {
			delete(s.forwards, id)
		}
		s.forwardsMu.Unlock()
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				problem(w, 404, "forward not found")
			} else {
				s.databaseError(w, err)
			}
			return
		}
		if run != nil {
			run.stop(forwardSockets{})
			s.publishUpdate("forwards", run.config.NodeID, id)
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	problem(w, 405, "method not allowed")
}

func forwardPortError(w http.ResponseWriter, detail string) {
	writeJSON(w, http.StatusConflict, map[string]string{
		"code":  "forward_port_unavailable",
		"error": detail,
	})
}
