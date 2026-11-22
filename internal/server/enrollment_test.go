package server

import (
	"database/sql"
	"errors"
	"path/filepath"
	"probex/internal/protocol"
	"sync"
	"testing"
	"time"
)

func TestEnrollmentAtomicBindingExpiryAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	n := nodeRow{Node: protocol.Node{Name: "reserved", Admission: "invited", JoinMode: "key"}}
	secret := hash(protocol.RandomID())
	if err := s.saveEnrollment(&n, secret, time.Now().Add(time.Hour), true); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if valid, err := s.enrollmentValid(n.ID, secret); err != nil || !valid {
		t.Fatal("pending enrollment not persisted", err)
	}
	previous := secret
	secret = hash(protocol.RandomID())
	if err := s.saveEnrollment(&n, secret, time.Now().Add(time.Hour), false); err != nil {
		t.Fatal(err)
	}
	if valid, err := s.enrollmentValid(n.ID, previous); err != nil || valid {
		t.Fatal("reissued key did not invalidate the previous key", err)
	}
	if err := s.activateNode(n.ID, hash("key"), hash("wrong-token")); !errors.Is(err, errEnrollment) {
		t.Fatal("invalid token bound a node", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []string{"first", "second"} {
		wg.Add(1)
		go func(key string) { defer wg.Done(); results <- s.activateNode(n.ID, hash(key), secret) }(key)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, errEnrollment) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("got %d successful bindings", success)
	}
	bound, err := s.node(n.ID)
	if err != nil || bound.Admission != "approved" || bound.KeyHash == "" {
		t.Fatal("binding not persisted", err)
	}
	if valid, err := s.enrollmentValid(n.ID, secret); err != nil || valid {
		t.Fatal("key was not consumed", err)
	}
	if err := s.saveEnrollment(&n, secret, time.Now().Add(time.Hour), false); !errors.Is(err, errAdmission) {
		t.Fatal("bound node accepted a replacement key", err)
	}
	n = nodeRow{Node: protocol.Node{Name: "expired", Admission: "invited", JoinMode: "key"}}
	if err := s.saveEnrollment(&n, secret, time.Now().Add(-time.Hour), true); err != nil {
		t.Fatal(err)
	}
	if err := s.activateNode(n.ID, hash("key"), secret); !errors.Is(err, errEnrollment) {
		t.Fatal("expired key accepted", err)
	}
	n = nodeRow{Node: protocol.Node{Name: "revoked", Admission: "invited", JoinMode: "key"}}
	if err := s.saveEnrollment(&n, secret, time.Now().Add(time.Hour), true); err != nil {
		t.Fatal(err)
	}
	if err := s.deleteNode(n.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.activateNode(n.ID, hash("key"), secret); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("revoked pending node accepted a key", err)
	}
}

func TestRenamePersistsNameAndUniqueIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rename.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	n := nodeRow{Node: protocol.Node{Name: "original", Admission: "approved", JoinMode: "key"}, KeyHash: hash("identity")}
	if err := s.insertNode(&n); err != nil {
		t.Fatal(err)
	}
	if err := s.updateNode(n.ID, func(node *protocol.Node) { node.Name = "renamed" }); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.node(n.ID)
	if err != nil || got.Name != "renamed" || got.KeyHash != n.KeyHash {
		t.Fatal("rename or identity not persisted", got, err)
	}
	other := nodeRow{Node: protocol.Node{Name: "original", Admission: "approved", JoinMode: "key"}}
	if err := s.insertNode(&other); err != nil {
		t.Fatal("old name not released", err)
	}
	if err := s.updateNode(other.ID, func(node *protocol.Node) { node.Name = "renamed" }); err == nil {
		t.Fatal("duplicate name accepted")
	}
	other, err = s.node(other.ID)
	if err != nil || other.Name != "original" {
		t.Fatal("failed rename changed JSON", other, err)
	}
}
