package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"probex/internal/protocol"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type Config struct {
	Server, Name, State, CA  string
	JoinKey, EnrollmentToken string
	Rejoin                   bool
}
type identity struct {
	ID         string `json:"id"`
	Mode       string `json:"join_mode"`
	Invitation string `json:"invitation,omitempty"`
	Key        string `json:"key"`
	Name       string `json:"name,omitempty"`
	CA         string `json:"ca_pem,omitempty"`
	Enrolled   bool   `json:"enrolled"`
}
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

func loadIdentity(path, assignedID, name, ca, invitation string, rejoin bool) (identity, error) {
	var id identity
	data, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return id, err
	}
	if exists {
		if err = json.Unmarshal(data, &id); err != nil {
			return id, err
		}
		for _, value := range []string{id.Key} {
			b, e := hex.DecodeString(value)
			if e != nil || len(b) != 32 {
				return id, fmt.Errorf("invalid identity file")
			}
		}
		if !rejoin && assignedID == "" && id.ID != "" && !protocol.ValidNodeID(id.ID) {
			return id, fmt.Errorf("invalid node ID; use a new identity file")
		}
		if id.Mode != "key" && id.Mode != "request" && !rejoin && assignedID == "" {
			return id, fmt.Errorf("invalid identity format; use --rejoin or a new join key")
		}
		if name == "" {
			name = id.Name
		}
		if ca == "" {
			ca = id.CA
		}
		if assignedID != "" && (assignedID != id.ID || invitation != id.Invitation) {
			rejoin = true
		}
		if !rejoin {
			if assignedID == "" && name != id.Name {
				return id, fmt.Errorf("node name differs from saved identity; use --rejoin for a new registration")
			}
			if assignedID != "" {
				id.Mode = "key"
				id.Name = name
				id.CA = ca
				data, err = json.Marshal(id)
				if err != nil {
					return id, err
				}
				if err = os.WriteFile(path, data, 0600); err != nil {
					return id, err
				}
			}
			return id, nil
		}
	}
	if name == "" {
		return id, fmt.Errorf("first connection requires --key or --name")
	}
	id = identity{ID: assignedID, Key: protocol.RandomID(), Name: name, CA: ca, Mode: "key", Invitation: invitation}
	if id.ID == "" {
		id.Mode = "request"
	}
	data, err = json.Marshal(id)
	if err != nil {
		return id, err
	}
	flags := os.O_CREATE | os.O_EXCL | os.O_WRONLY
	if exists {
		flags = os.O_TRUNC | os.O_WRONLY
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return id, err
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err != nil {
		return id, err
	}
	return id, closeErr
}

func Run(ctx context.Context, c Config) error {
	if c.Server == "" || c.State == "" {
		return fmt.Errorf("explicit --server and state path required")
	}
	endpoint, err := protocol.Endpoint(c.Server, true)
	if err != nil {
		return err
	}
	var assignedID, ca string
	if c.JoinKey != "" {
		join, err := protocol.DecodeEnrollment(c.JoinKey)
		if err != nil {
			return fmt.Errorf("join key is invalid; copy the complete current key from the administrator: %w", err)
		}
		if c.Name != "" && c.Name != join.Name {
			return fmt.Errorf("--name differs from join key")
		}
		assignedID, c.Name, c.EnrollmentToken, ca = join.NodeID, join.Name, join.Token, join.CA
	}
	if c.CA != "" {
		data, err := os.ReadFile(c.CA)
		if err != nil {
			return fmt.Errorf("read CA file %s: %w", c.CA, err)
		}
		ca = string(data)
		if ca == "" {
			return fmt.Errorf("CA file %s is empty; provide a PEM CA certificate", c.CA)
		}
	}
	invitation := ""
	if c.EnrollmentToken != "" {
		digest := sha256.Sum256([]byte(c.EnrollmentToken))
		invitation = hex.EncodeToString(digest[:])
	}
	id, err := loadIdentity(c.State, assignedID, c.Name, ca, invitation, c.Rejoin)
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	if c.Name == "" {
		c.Name = id.Name
	}
	if c.Name == "" {
		return fmt.Errorf("node name required in the saved identity")
	}
	if ca == "" {
		ca = id.CA
	}
	id.Name, id.CA = c.Name, ca
	if id.Enrolled {
		c.EnrollmentToken = ""
	}
	client, err := protocol.HTTPClientWithCA([]byte(ca))
	if err != nil {
		return fmt.Errorf("invalid CA certificate: %w", err)
	}

	delay := time.Second
	for {
		var welcomed bool
		if !id.Enrolled && id.Mode == "request" {
			err = awaitApproval(ctx, c, &id, client)
			if err == nil && ctx.Err() == nil {
				delay = time.Second
				continue
			}
		} else {
			welcomed, err = connect(ctx, c, id, endpoint, client)
		}
		if ctx.Err() != nil {
			return nil
		}
		var certificate *tls.CertificateVerificationError
		var record tls.RecordHeaderError
		if errors.As(err, &certificate) || errors.As(err, &record) || errors.Is(err, http.ErrSchemeMismatch) {
			return connectionError(err, ca != "")
		}
		var permanent *permanentError
		if errors.As(err, &permanent) {
			return err
		}
		if welcomed {
			delay = time.Second
			c.EnrollmentToken = ""
			id.Enrolled = true
		}
		log.Printf("connection lost: %v; reconnect in %s", connectionError(err, ca != ""), delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		delay *= 2
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
}

type session struct {
	ctx         context.Context
	conn        *websocket.Conn
	commandBusy chan struct{}
	wg          sync.WaitGroup
	mu          sync.Mutex
	terminal    *terminalSession
}

func connect(parent context.Context, c Config, id identity, endpoint string, client *http.Client) (bool, error) {
	dial, cancel := context.WithTimeout(parent, 10*time.Second)
	header := http.Header{"Authorization": []string{"Bearer " + id.Key}, "X-Probex-ID": []string{id.ID}}
	if c.EnrollmentToken != "" {
		header.Set("X-Enrollment-Token", c.EnrollmentToken)
	}
	conn, resp, err := websocket.Dial(dial, endpoint, &websocket.DialOptions{HTTPClient: client, HTTPHeader: header})
	cancel()
	if err != nil {
		if resp != nil && resp.Body != nil && resp.StatusCode >= 300 {
			defer resp.Body.Close()
			return false, rejectedResponse(resp)
		}
		return false, err
	}
	ctx, stop := context.WithCancel(parent)
	s := &session{ctx: ctx, conn: conn, commandBusy: make(chan struct{}, 1)}
	defer func() { stop(); conn.CloseNow(); s.wg.Wait() }()
	conn.SetReadLimit(protocol.MaxMessage)
	info := Collect(ctx)
	if err := s.send(protocol.Message{Type: "hello", Version: protocol.WireVersion, Name: c.Name, Info: &info}); err != nil {
		return false, err
	}
	var welcome protocol.Message
	read, done := context.WithTimeout(ctx, 10*time.Second)
	err = wsjson.Read(read, conn, &welcome)
	done()
	if err != nil {
		if websocket.CloseStatus(err) == websocket.StatusPolicyViolation {
			return false, &permanentError{fmt.Errorf("node registration rejected: %w", err)}
		}
		return false, err
	}
	if welcome.Type != "welcome" || welcome.Version != protocol.WireVersion {
		return false, &permanentError{fmt.Errorf("unsupported welcome")}
	}
	if !id.Enrolled {
		id.Enrolled = true
		data, err := json.Marshal(id)
		if err != nil {
			return false, &permanentError{err}
		}
		if err := os.WriteFile(c.State, data, 0600); err != nil {
			return false, &permanentError{fmt.Errorf("save enrolled identity: %w", err)}
		}
	}
	log.Printf("connected as %s (%s)", c.Name, id.ID)
	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ping, done := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(ping)
				done()
				if err != nil {
					stop()
					return
				}
			}
		}
	}()
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				info := Collect(ctx)
				if err := s.send(protocol.Message{Type: "info", Info: &info}); err != nil {
					stop()
					return
				}
			}
		}
	}()
	for {
		var m protocol.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			if websocket.CloseStatus(err) == websocket.StatusPolicyViolation {
				return true, &permanentError{fmt.Errorf("node deleted or access revoked; use --rejoin or a new join key to request admission again")}
			}
			return true, err
		}
		if m.Type == "term_open" {
			if err := s.openTerminal(m); err != nil {
				return true, err
			}
			continue
		}
		if m.Type == "term_input" || m.Type == "term_resize" || m.Type == "term_close" {
			if err := s.terminalControl(m); err != nil {
				return true, err
			}
			continue
		}
		if m.Type != "exec" {
			return true, &permanentError{fmt.Errorf("unsupported server message %q", m.Type)}
		}
		if m.ID == "" || m.Command == "" || len(m.Command) > 16<<10 || m.TimeoutSeconds < 1 || m.TimeoutSeconds > 3600 {
			return true, &permanentError{fmt.Errorf("invalid command")}
		}
		select {
		case s.commandBusy <- struct{}{}:
			s.wg.Add(1)
			go func() { defer s.wg.Done(); s.execute(m) }()
		default:
			return true, fmt.Errorf("server sent concurrent command")
		}
	}
}
func (s *session) send(m protocol.Message) error {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	return wsjson.Write(ctx, s.conn, m)
}
