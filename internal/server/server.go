package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"probex/internal/protocol"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type Config struct {
	AdminToken        string
	AllowRegistration bool
	AutoApprove       bool
	Port              int
	CA                string
	EnrollmentTTL     time.Duration
}
type peer struct {
	conn     *websocket.Conn
	command  string
	terminal *terminalPeer
}
type Server struct {
	store     *Store
	config    Config
	mu        sync.Mutex
	peers     map[string]*peer
	sessions  map[string]time.Time
	terminals map[*websocket.Conn]string
	agents    sync.WaitGroup
	closed    bool
}

func New(store *Store, config Config) (*Server, error) {
	if config.AdminToken == "" {
		return nil, fmt.Errorf("admin token required")
	}
	if config.EnrollmentTTL == 0 {
		config.EnrollmentTTL = 24 * time.Hour
	}
	if config.EnrollmentTTL < 0 {
		return nil, fmt.Errorf("enrollment TTL must be positive")
	}
	return &Server{store: store, config: config, peers: map[string]*peer{}, sessions: map[string]time.Time{}, terminals: map[*websocket.Conn]string{}}, nil
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /agent", s.agent)
	mux.HandleFunc("POST /register", s.registration)
	mux.HandleFunc("GET /registration", s.registration)
	mux.HandleFunc("POST /api/login", s.login)
	mux.Handle("/api/", s.auth(http.HandlerFunc(s.api)))
	mux.Handle("/", http.HandlerFunc(s.page))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write response: %v", err)
	}
}
func problem(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, protocol.MaxMessage)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		problem(w, 400, "invalid JSON body")
		return false
	}
	return true
}
func equal(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}
func hash(value string) string { h := sha256.Sum256([]byte(value)); return hex.EncodeToString(h[:]) }
func sameOrigin(r *http.Request) bool {
	u, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host == r.Host
}
func secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	return err == nil && net.ParseIP(host).IsLoopback() && r.Header.Get("X-Forwarded-Proto") == "https"
}

func sessionCookieName(r *http.Request) string {
	if secureRequest(r) {
		return "probex_session"
	}
	return "probex_session_http"
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" && !sameOrigin(r) {
		problem(w, 403, "origin rejected")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !equal(body.Token, s.config.AdminToken) {
		problem(w, 401, "invalid token")
		return
	}
	id := protocol.RandomID()
	now := time.Now()
	s.mu.Lock()
	for key, expiry := range s.sessions {
		if !expiry.After(now) {
			delete(s.sessions, key)
		}
	}
	s.sessions[id] = now.Add(12 * time.Hour)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName(r), Value: id, Path: "/", HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteStrictMode, MaxAge: 43200})
	writeJSON(w, 200, map[string]bool{"authenticated": true})
}

type sessionKey struct{}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		admin := bearer != "" && equal(bearer, s.config.AdminToken)
		if !admin {
			cookie, err := r.Cookie(sessionCookieName(r))
			if err != nil {
				problem(w, 401, "login required")
				return
			}
			s.mu.Lock()
			expiry := s.sessions[cookie.Value]
			s.mu.Unlock()
			if !expiry.After(time.Now()) {
				problem(w, 401, "session expired")
				return
			}
			if r.Method != "GET" && !sameOrigin(r) {
				problem(w, 403, "origin rejected")
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), sessionKey{}, cookie.Value))
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	if path == "nodes" && r.Method == "POST" {
		s.issueEnrollment(w, r, "")
		return
	}
	if path == "session" && r.Method == "GET" {
		writeJSON(w, 200, map[string]bool{"authenticated": true})
		return
	}
	if path == "logout" && r.Method == "POST" {
		if c, err := r.Cookie(sessionCookieName(r)); err == nil {
			s.mu.Lock()
			delete(s.sessions, c.Value)
			var terminals []*websocket.Conn
			for conn, sessionID := range s.terminals {
				if sessionID == c.Value {
					terminals = append(terminals, conn)
				}
			}
			s.mu.Unlock()
			for _, conn := range terminals {
				conn.CloseNow()
			}
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookieName(r), Value: "", Path: "/", HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteStrictMode, MaxAge: -1})
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if path == "nodes" && r.Method == "GET" {
		nodes, err := s.store.nodes()
		if err != nil {
			s.databaseError(w, err)
			return
		}
		s.mu.Lock()
		for i := range nodes {
			nodes[i].SetStatus(s.peers[nodes[i].ID] != nil)
		}
		s.mu.Unlock()
		writeJSON(w, 200, nodes)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[0] == "commands" && r.Method == "GET" {
		c, err := s.store.command(parts[1])
		if err != nil {
			s.lookupError(w, err)
			return
		}
		writeJSON(w, 200, c)
		return
	}
	if len(parts) < 2 || parts[0] != "nodes" {
		problem(w, 404, "not found")
		return
	}
	id := parts[1]
	n, err := s.store.node(id)
	if err != nil {
		s.lookupError(w, err)
		return
	}
	if n.Admission == "deleted" {
		problem(w, 404, "node removed")
		return
	}
	if len(parts) == 3 && (parts[2] == "approve" || parts[2] == "reject") && r.Method == "POST" {
		s.reviewNode(w, r, id, parts[2])
		return
	}
	if len(parts) == 3 && parts[2] == "enrollment" && r.Method == "POST" {
		s.issueEnrollment(w, r, id)
		return
	}
	if len(parts) == 2 {
		switch r.Method {
		case "GET":
			s.mu.Lock()
			n.SetStatus(s.peers[id] != nil)
			s.mu.Unlock()
			writeJSON(w, 200, n.Node)
		case "PATCH":
			var body struct {
				Name string `json:"name"`
			}
			if !decode(w, r, &body) {
				return
			}
			if !validName(body.Name) {
				problem(w, 400, "invalid node name")
				return
			}
			if err := s.store.updateNode(id, func(node *protocol.Node) { node.Name = body.Name }); err != nil {
				s.nodeError(w, err)
				return
			}
			n.Name = body.Name
			s.mu.Lock()
			n.SetStatus(s.peers[id] != nil)
			s.mu.Unlock()
			writeJSON(w, 200, n.Node)
		case "DELETE":
			s.mu.Lock()
			err = s.store.deleteNode(id)
			if err != nil {
				s.mu.Unlock()
				s.nodeError(w, err)
				return
			}
			p := s.peers[id]
			delete(s.peers, id)
			if p != nil {
				p.command = ""
				if p.terminal != nil {
					p.terminal.conn.CloseNow()
					p.terminal = nil
				}
			}
			s.mu.Unlock()
			if p != nil {
				p.conn.Close(websocket.StatusPolicyViolation, "node revoked")
			}
			writeJSON(w, 200, map[string]bool{"ok": true})
		default:
			problem(w, 405, "method not allowed")
		}
		return
	}
	if len(parts) == 3 && parts[2] == "commands" {
		switch r.Method {
		case "DELETE":
			s.mu.Lock()
			err := s.store.clearHistory(id)
			var history []protocol.Command
			if err == nil {
				history, err = s.store.history(id, 0)
			}
			s.mu.Unlock()
			if err != nil {
				s.databaseError(w, err)
				return
			}
			for i := range history {
				history[i].Stdout = nil
				history[i].Stderr = nil
			}
			writeJSON(w, 200, history)
		case "GET":
			offset := 0
			if raw := r.URL.Query().Get("offset"); raw != "" {
				offset, err = strconv.Atoi(raw)
				if err != nil || offset < 0 {
					problem(w, 400, "invalid offset")
					return
				}
			}
			history, err := s.store.history(id, offset)
			if err != nil {
				s.databaseError(w, err)
				return
			}
			for i := range history {
				history[i].Stdout = nil
				history[i].Stderr = nil
			}
			writeJSON(w, 200, history)
		case "POST":
			s.execute(w, r, id)
		default:
			problem(w, 405, "method not allowed")
		}
		return
	}
	if len(parts) == 3 && parts[2] == "terminal" && r.Method == "GET" {
		s.terminal(w, r, id)
		return
	}
	problem(w, 404, "not found")
}
func (s *Server) databaseError(w http.ResponseWriter, err error) {
	log.Printf("database: %v", err)
	problem(w, 500, "database operation failed")
}
func (s *Server) lookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		problem(w, 404, "not found")
	} else {
		s.databaseError(w, err)
	}
}
func validName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}
func validKey(key string) bool { b, err := hex.DecodeString(key); return err == nil && len(b) == 32 }
func (s *Server) agent(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		problem(w, 503, "server stopping")
		return
	}
	s.agents.Add(1)
	s.mu.Unlock()
	defer s.agents.Done()
	id := r.Header.Get("X-Probex-ID")
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !protocol.ValidNodeID(id) || !validKey(key) {
		problem(w, 401, "invalid node identity")
		return
	}
	n, err := s.store.node(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			problem(w, 403, "node not registered")
		} else {
			s.databaseError(w, err)
		}
		return
	}
	allowed := n.Admission == "approved" && equal(n.KeyHash, hash(key))
	if n.Admission == "invited" {
		allowed, err = s.store.enrollmentValid(id, hash(r.Header.Get("X-Enrollment-Token")))
		if err != nil {
			s.databaseError(w, err)
			return
		}
	}
	if !allowed {
		message := "invalid node credentials; use the original state file or request a new join key"
		switch n.Admission {
		case "invited":
			message = "invalid join key; request the current key from the administrator"
		case "expired":
			message = "join key expired; request a new join key"
		case "review":
			message = "registration awaiting approval; wait for administrator approval"
		case "rejected":
			message = "registration rejected; use --rejoin or a new join key to request admission again"
		case "deleted":
			message = "node deleted; use --rejoin or a new join key to request admission again"
		case "approved":
			if r.Header.Get("X-Enrollment-Token") != "" {
				message = "join key already used or invalid; reuse the original state file or request a new join key"
			}
		}
		problem(w, 403, message)
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		log.Printf("upgrade: %v", err)
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(protocol.MaxMessage)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	var hello protocol.Message
	first, stop := context.WithTimeout(ctx, 10*time.Second)
	err = wsjson.Read(first, conn, &hello)
	stop()
	if err != nil || hello.Type != "hello" || !validName(hello.Name) || hello.Info == nil {
		conn.Close(websocket.StatusPolicyViolation, "invalid hello")
		return
	}
	if hello.Version != protocol.WireVersion {
		conn.Close(websocket.StatusPolicyViolation, fmt.Sprintf("protocol version %d required; upgrade server and client together", protocol.WireVersion))
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.CloseNow()
		return
	}
	n, err = s.store.node(id)
	if err == nil && n.Admission == "invited" {
		err = s.store.activateNode(id, hash(key), hash(r.Header.Get("X-Enrollment-Token")))
	} else if err == nil && (n.Admission != "approved" || !equal(n.KeyHash, hash(key))) {
		err = errIdentity
	}

	if err != nil {
		s.mu.Unlock()
		log.Printf("register: %v", err)
		conn.Close(websocket.StatusPolicyViolation, "registration rejected")
		return
	}
	p := &peer{conn: conn}
	old := s.peers[id]
	s.peers[id] = p
	s.mu.Unlock()
	if old != nil {
		old.conn.Close(websocket.StatusGoingAway, "connection replaced")
	}
	defer func() {
		s.mu.Lock()
		if s.peers[id] == p {
			delete(s.peers, id)
		}
		active := p.command
		terminal := p.terminal
		s.mu.Unlock()
		if terminal != nil {
			terminal.conn.CloseNow()
		}
		if active != "" {
			if err := s.unknown(active, "connection lost before result"); err != nil {
				log.Printf("persist disconnect: %v", err)
			}
		}
	}()
	if err := send(ctx, conn, protocol.Message{Type: "welcome", Version: protocol.WireVersion, ID: id}); err != nil {
		return
	}
	if err := s.store.updateNode(id, func(n *protocol.Node) { n.Info = *hello.Info; n.LastSeen = time.Now().UTC() }); err != nil {
		log.Printf("persist info: %v", err)
		return
	}
	for {
		var m protocol.Message
		deadline, done := context.WithTimeout(ctx, 65*time.Second)
		err := wsjson.Read(deadline, conn, &m)
		done()
		if err != nil {
			return
		}
		if m.Type == "info" && m.Info != nil {
			err = s.store.updateNode(id, func(n *protocol.Node) { n.Info = *m.Info; n.LastSeen = time.Now().UTC() })
		} else {
			err = s.commandMessage(p, m)
		}
		if err != nil {
			log.Printf("node message: %v", err)
			return
		}
	}
}
func send(ctx context.Context, conn *websocket.Conn, m protocol.Message) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return wsjson.Write(ctx, conn, m)
}
func (s *Server) unknown(id, reason string) error {
	err := s.store.updateCommand(id, func(c *protocol.Command) {
		if !protocol.Terminal(c.Status) {
			now := time.Now().UTC()
			c.Status = "unknown"
			c.Error = reason
			c.FinishedAt = &now
		}
	})
	// Node deletion may remove the record while disconnect cleanup is in flight.
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	list := make([]*peer, 0, len(s.peers))
	for _, p := range s.peers {
		list = append(list, p)
	}
	s.mu.Unlock()
	for _, p := range list {
		p.conn.CloseNow()
	}
	s.agents.Wait()
}
