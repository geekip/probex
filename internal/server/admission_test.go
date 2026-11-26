package server

import (
	"database/sql"
	"errors"
	"path/filepath"
	"probex/internal/protocol"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAdmissionNameReuseAndConcurrency(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "names.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key := hash(protocol.RandomID())
	first, err := s.registerNode("shared-name", key, protocol.Info{}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.registerNode("shared-name", key, protocol.Info{}, false, false)
	if err != nil || same.ID != first.ID || same.Admission != "review" {
		t.Fatal("request not idempotent", same, err)
	}
	if _, err = s.registerNode("shared-name", hash("wrong"), protocol.Info{}, true, false); !errors.Is(err, errNameExists) {
		t.Fatal("identity takeover", err)
	}
	if _, err = s.registerNode("shared-name", hash("new"), protocol.Info{}, true, false); !errors.Is(err, errNameExists) {
		t.Fatal("duplicate waiting name accepted", err)
	}
	if err = s.updateNode(first.ID, func(n *protocol.Node) { n.Admission = "rejected" }); err != nil {
		t.Fatal(err)
	}
	same, err = s.registerNode("shared-name", key, protocol.Info{}, true, false)
	if err != nil || same.Admission != "rejected" {
		t.Fatal("rejection bypassed", same, err)
	}
	invitation := nodeRow{Node: protocol.Node{Name: "shared-name", CreatedAt: time.Now().UTC()}}
	if err = s.saveEnrollment(&invitation, hash("join"), time.Now().Add(-time.Second), true); err != nil {
		t.Fatal(err)
	}
	expired, err := s.node(invitation.ID)
	if err != nil || expired.Admission != "expired" {
		t.Fatal("expiry not reflected", expired, err)
	}
	active, err := s.registerNode("shared-name", hash("active"), protocol.Info{}, true, false)
	if err != nil {
		t.Fatal("expired name not released", err)
	}
	if err = s.saveEnrollment(&invitation, hash("replacement"), time.Now().Add(time.Hour), false); !errors.Is(err, errNameExists) {
		t.Fatal("expired row stole name", err)
	}
	if err = s.deleteNode(active.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.saveEnrollment(&invitation, hash("replacement"), time.Now().Add(time.Hour), false); err != nil {
		t.Fatal("deleted name not released", err)
	}
	if err = s.activateNode(invitation.ID, hash("device"), hash("join")); !errors.Is(err, errEnrollment) {
		t.Fatal("old key resurrected", err)
	}
	if err = s.activateNode(invitation.ID, hash("device"), hash("replacement")); err != nil {
		t.Fatal(err)
	}
	approved, err := s.node(invitation.ID)
	if err != nil || approved.Admission != "approved" || approved.JoinedAt == nil || approved.KeyExpiresAt != nil {
		t.Fatal("binding timestamp", approved, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.registerNode("concurrent", hash(protocol.RandomID()), protocol.Info{}, true, false)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, errNameExists) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("concurrent uniqueness", success)
	}
}

func TestAutomaticApprovalOnlyAppliesToNewRequests(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "auto.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key := hash(protocol.RandomID())
	n, err := s.registerNode("automatic", key, protocol.Info{}, true, true)
	if err != nil || n.Admission != "approved" || n.JoinedAt == nil {
		t.Fatal("new request not automatically approved", n, err)
	}
	if _, err := s.registerNode("automatic", hash(protocol.RandomID()), protocol.Info{}, true, true); !errors.Is(err, errNameExists) {
		t.Fatal("automatic approval bypassed name uniqueness", err)
	}
	if _, err := s.registerNode("closed", hash(protocol.RandomID()), protocol.Info{}, false, true); !errors.Is(err, errRegistrationClosed) {
		t.Fatal("automatic approval bypassed disabled registration", err)
	}
	if err := s.deleteNode(n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.node(n.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted credentials retained", err)
	}
	reviewKey := hash(protocol.RandomID())
	n, err = s.registerNode("manual", reviewKey, protocol.Info{}, true, false)
	if err != nil || n.Admission != "review" || n.JoinedAt != nil {
		t.Fatal(n, err)
	}
	n, err = s.registerNode("manual", reviewKey, protocol.Info{}, true, true)
	if err != nil || n.Admission != "review" || n.JoinedAt != nil {
		t.Fatal("existing manual review silently approved", n, err)
	}
}

func TestOldDatabaseExplicitlyRejectedAndRetained(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE nodes(id TEXT PRIMARY KEY,name TEXT UNIQUE,body TEXT); INSERT INTO nodes VALUES('old','old','retain')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := OpenStore(path); err == nil {
		s.Close()
		t.Fatal("old database silently accepted")
	} else if !strings.Contains(err.Error(), "new --db") {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var body string
	if err = db.QueryRow(`SELECT body FROM nodes WHERE id='old'`).Scan(&body); err != nil || body != "retain" {
		t.Fatal("old data changed", body, err)
	}
}

func TestAutoincrementPersistsAndRequestsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sequence.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	key := hash(protocol.RandomID())
	first, err := s.registerNode("request", key, protocol.Info{}, true, false)
	if err != nil || first.ID != "1" {
		t.Fatal(first, err)
	}
	invitation := nodeRow{Node: protocol.Node{Name: "invitation"}}
	if err = s.saveEnrollment(&invitation, hash("invite"), time.Now().Add(time.Hour), true); err != nil || invitation.ID != "2" {
		t.Fatal(invitation, err)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := s.registerNode("request", key, protocol.Info{}, true, false)
			if err != nil || n.ID != first.ID {
				t.Error(n, err)
			}
		}()
	}
	wg.Wait()
	if err = s.deleteNode(invitation.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	replacement := nodeRow{Node: protocol.Node{Name: "invitation"}}
	if err = s.saveEnrollment(&replacement, hash("replacement"), time.Now().Add(time.Hour), true); err != nil || replacement.ID != "3" {
		t.Fatal(replacement, err)
	}
	n, err := s.registerNode("request", key, protocol.Info{}, false, false)
	if err != nil || n.ID != "1" {
		t.Fatal(n, err)
	}
	nodes, err := s.nodes(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range nodes {
		if _, err := strconv.ParseInt(node.ID, 10, 64); err != nil {
			t.Fatal(node.ID)
		}
	}
}
