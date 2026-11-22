package server

import (
	"database/sql"
	"errors"
	"net/http"
	"probex/internal/protocol"
	"time"
)

var errEnrollment = errors.New("join key expired, consumed or invalid")

func (s *Store) saveEnrollment(n *nodeRow, tokenHash string, expires time.Time, create bool) error {
	if !create {
		_, err := s.changeNode(n.ID, func(tx *sql.Tx, current *nodeRow) error {
			if current.JoinMode != "key" || (current.Admission != "invited" && current.Admission != "expired") {
				return errAdmission
			}
			current.Admission = "invited"
			current.KeyExpiresAt = &expires
			_, err := tx.Exec(`INSERT INTO enrollments(node_id,token_hash,expires_at) VALUES(?,?,?) ON CONFLICT(node_id) DO UPDATE SET token_hash=excluded.token_hash,expires_at=excluded.expires_at`, current.ID, tokenHash, expires.UTC().Format(timestampFormat))
			return err
		})
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = expireKeys(tx); err != nil {
		return err
	}
	n.JoinMode = "key"
	n.Admission = "invited"
	n.KeyExpiresAt = &expires
	if err = persistNode(tx, n, true); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO enrollments(node_id,token_hash,expires_at) VALUES(?,?,?)`, n.ID, tokenHash, expires.UTC().Format(timestampFormat)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) enrollmentValid(id, tokenHash string) (bool, error) {
	var stored, expiry string
	err := s.db.QueryRow(`SELECT token_hash,expires_at FROM enrollments WHERE node_id=?`, id).Scan(&stored, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	deadline, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil {
		return false, err
	}
	return time.Now().Before(deadline) && equal(stored, tokenHash), nil
}

func (s *Store) activateNode(id, keyHash, tokenHash string) error {
	_, err := s.changeNode(id, func(tx *sql.Tx, n *nodeRow) error {
		if n.Admission != "invited" {
			return errEnrollment
		}
		var stored string
		if err := tx.QueryRow(`SELECT token_hash FROM enrollments WHERE node_id=?`, id).Scan(&stored); err != nil {
			return err
		}
		if !equal(stored, tokenHash) {
			return errEnrollment
		}
		now := time.Now().UTC()
		n.Admission = "approved"
		n.JoinedAt = &now
		n.KeyExpiresAt = nil
		n.KeyHash = keyHash
		_, err := tx.Exec(`DELETE FROM enrollments WHERE node_id=?`, id)
		return err
	})
	return err
}

func (s *Server) issueEnrollment(w http.ResponseWriter, r *http.Request, id string) {
	var n nodeRow
	var err error
	create := id == ""
	if create {
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
		n = nodeRow{Node: protocol.Node{Name: body.Name, CreatedAt: time.Now().UTC()}}
	} else {
		n, err = s.store.node(id)
		if err != nil {
			s.lookupError(w, err)
			return
		}
	}
	join := protocol.Enrollment{Version: 1, Port: s.config.Port, Name: n.Name, Token: protocol.RandomID(), CA: s.config.CA, ExpiresAt: time.Now().UTC().Add(s.config.EnrollmentTTL)}
	if err = s.store.saveEnrollment(&n, hash(join.Token), join.ExpiresAt, create); err != nil {
		s.nodeError(w, err)
		return
	}
	join.NodeID = n.ID
	n, err = s.store.node(n.ID)
	if err != nil {
		s.databaseError(w, err)
		return
	}
	n.SetStatus(false)
	key, err := join.Encode()
	if err != nil {
		s.databaseError(w, err)
		return
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	writeJSON(w, status, struct {
		Node      protocol.Node `json:"node"`
		Key       string        `json:"key"`
		ExpiresAt time.Time     `json:"expires_at"`
	}{n.Node, key, join.ExpiresAt})
}
