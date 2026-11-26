package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"probex/internal/agent"
	"probex/internal/protocol"
	"sync"
	"time"
)

const localNodeName = "Probexd-Local"

func (s *Server) nodeEnabled(id string) bool {
	if id == protocol.LocalNodeID {
		return s.config.LocalNode
	}
	return protocol.ValidNodeID(id)
}

func (s *Store) ensureLocalNode() error {
	now := time.Now().UTC()
	node := protocol.Node{ID: protocol.LocalNodeID, Name: localNodeName, JoinMode: "local", Admission: "approved", CreatedAt: now, JoinedAt: &now}
	data, err := json.Marshal(node)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO nodes(id,name,admission,key_hash,body) VALUES(?,?,?,'',?) ON CONFLICT(id) DO NOTHING`,
		node.ID, node.Name, node.Admission, string(data))
	return err
}

func (s *Server) startLocalNode() error {
	if err := s.store.ensureLocalNode(); err != nil {
		return fmt.Errorf("create built-in node: %w", err)
	}
	node, err := s.store.node(protocol.LocalNodeID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.localCancel = cancel
	s.localReady = make(chan struct{})
	s.localDone = make(chan struct{})
	listener := &localListener{incoming: make(chan net.Conn), closed: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent", func(w http.ResponseWriter, r *http.Request) {
		s.serveAgent(w, r, protocol.LocalNodeID, "")
	})
	mux.HandleFunc("GET /agent/data", func(w http.ResponseWriter, r *http.Request) {
		s.serveAgentData(w, r, protocol.LocalNodeID, "")
	})
	service := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	transport := &http.Transport{DialContext: listener.dial}
	go func() {
		if err := service.Serve(listener); err != http.ErrServerClosed {
			log.Printf("built-in node transport: %v", err)
		}
	}()
	go func() {
		defer close(s.localDone)
		defer service.Close()
		defer transport.CloseIdleConnections()
		if err := agent.RunLocal(ctx, &http.Client{Transport: transport}, node.Name); err != nil {
			log.Printf("built-in node: %v", err)
		}
	}()

	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case <-s.localReady:
		return nil
	case <-s.localDone:
		return fmt.Errorf("built-in node stopped before startup")
	case <-timer.C:
		return fmt.Errorf("built-in node startup timed out")
	}
}

// localListener gives the shared Agent protocol a private transport with no
// listening socket. Only the built-in Agent's HTTP client can dial it.
type localListener struct {
	incoming chan net.Conn
	closed   chan struct{}
	once     sync.Once
}

func (l *localListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.incoming:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *localListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *localListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "probexd-local", Net: "unix"}
}

func (l *localListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.incoming <- server:
		return client, nil
	case <-l.closed:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	}
}
