package server

import (
	"database/sql"
	"errors"
	"net/http"
	"probex/internal/protocol"
	"strings"
	"time"
)

var errRegistrationClosed = errors.New("registration disabled")

func (s *Store) registerNode(name, keyHash string, info protocol.Info, enabled, autoApprove bool) (nodeRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return nodeRow{}, err
	}
	defer tx.Rollback()
	if err = expireKeys(tx); err != nil {
		return nodeRow{}, err
	}
	n, err := readNode(tx.QueryRow(`SELECT body,key_hash FROM nodes WHERE key_hash=?`, keyHash))
	if err == nil {
		if n.JoinMode != "request" {
			return n, errIdentity
		}
		return n, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return n, err
	}
	if !enabled {
		return n, errRegistrationClosed
	}
	n = nodeRow{Node: protocol.Node{Name: name, JoinMode: "request", Admission: "review", Info: info, CreatedAt: time.Now().UTC()}, KeyHash: keyHash}
	if autoApprove {
		n.Admission = "approved"
		n.JoinedAt = &n.CreatedAt
	}
	if err = persistNode(tx, &n, true); err != nil {
		return n, err
	}
	return n, tx.Commit()
}

func (s *Server) registration(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-Probex-ID")
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !validKey(key) || ((id != "" || r.Method == http.MethodGet) && !protocol.ValidNodeID(id)) {
		problem(w, 401, "invalid node identity")
		return
	}
	var n nodeRow
	var err error
	if r.Method == http.MethodPost {
		var body struct {
			Name string        `json:"name"`
			Info protocol.Info `json:"info"`
		}
		if !decode(w, r, &body) {
			return
		}
		if !validName(body.Name) {
			problem(w, 400, "invalid node name")
			return
		}
		if id == "" {
			n, err = s.store.registerNode(body.Name, hash(key), body.Info, s.config.AllowRegistration, s.config.AutoApprove)
		}
	}
	if id != "" {
		n, err = s.store.node(id)
		if errors.Is(err, sql.ErrNoRows) {
			problem(w, 403, "node not registered")
			return
		}
		if err == nil && (n.JoinMode != "request" || !equal(n.KeyHash, hash(key))) {
			err = errIdentity
		}
	}
	if err != nil {
		if errors.Is(err, errRegistrationClosed) {
			problem(w, 403, err.Error())
		} else {
			s.nodeError(w, err)
		}
		return
	}
	n.SetStatus(false)
	status := http.StatusOK
	if n.Admission == "review" {
		status = http.StatusAccepted
	}
	if n.Admission == "rejected" || n.Admission == "deleted" {
		status = http.StatusForbidden
	}
	if r.Method == http.MethodPost && id == "" {
		s.publishUpdate("nodes", n.ID, "")
	}
	writeJSON(w, status, n.Node)
}

func (s *Server) reviewNode(w http.ResponseWriter, r *http.Request, id, action string) {
	n, err := s.store.changeNode(id, func(_ *sql.Tx, n *nodeRow) error {
		if n.JoinMode != "request" || n.Admission != "review" {
			return errAdmission
		}
		n.Admission = "rejected"
		if action == "approve" {
			now := time.Now().UTC()
			n.Admission = "approved"
			n.JoinedAt = &now
		}
		return nil
	})
	if err != nil {
		s.nodeError(w, err)
		return
	}
	n.SetStatus(false)
	s.publishUpdate("nodes", id, "")
	writeJSON(w, http.StatusOK, n.Node)
}

func (s *Server) nodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNameExists):
		problem(w, 409, err.Error())
	case errors.Is(err, errAdmission), errors.Is(err, errEnrollment):
		problem(w, 409, err.Error())
	case errors.Is(err, errIdentity):
		problem(w, 403, err.Error())
	case errors.Is(err, sql.ErrNoRows):
		s.lookupError(w, err)
	default:
		s.databaseError(w, err)
	}
}
