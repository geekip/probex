package server

import (
	"path/filepath"
	"probex/internal/protocol"
	"testing"
)

func TestForwardMigrationPreservesNodesAndCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	n := nodeRow{Node: protocol.Node{Name: "retained", Admission: "approved"}, KeyHash: hash("secret")}
	if err := s.insertNode(&n); err != nil {
		t.Fatal(err)
	}
	if err := s.insertCommand(protocol.Command{ID: "saved", NodeID: n.ID, Command: "echo retained", Status: "succeeded", Stdout: []byte("retained")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE forwards; PRAGMA user_version=2;`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.node(n.ID)
	if err != nil || got.KeyHash != n.KeyHash {
		t.Fatal(got, err)
	}
	command, err := s.command("saved")
	if err != nil || string(command.Stdout) != "retained" {
		t.Fatal(command, err)
	}
	f := Forward{NodeID: n.ID, Protocol: "tcp", ListenAddr: "127.0.0.1:2222", TargetHost: "127.0.0.1", TargetPort: 22}
	if err := s.insertForward(&f); err != nil {
		t.Fatal(err)
	}
	if err := s.deleteNode(n.ID); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM forwards").Scan(&rows); err != nil || rows != 0 {
		t.Fatal("forward rows retained", rows, err)
	}
	list, err := s.forwards(true)
	if err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
}
func TestForwardTargetParsing(t *testing.T) {
	for _, target := range []string{"node:22", "1:192.168.1.10:80", "node:[::1]:443"} {
		pairs, err := parseForwardPairs([]string{"127.0.0.1:2222", target}, "tcp")
		if err != nil || len(pairs) != 1 {
			t.Fatal(target, pairs, err)
		}
	}
	for _, args := range [][]string{nil, {"127.0.0.1:22"}, {"127.0.0.1:0", "node:22"}, {"bad", "node:22"}, {"127.0.0.1:22", "node:0"}, {"127.0.0.1:22", "node:::1:22"}, {"127.0.0.1:22", "node:host:not-port"}} {
		if _, err := parseForwardPairs(args, "tcp"); err == nil {
			t.Fatal("invalid pair accepted", args)
		}
	}
}

func TestForwardNamedMigrationPreservesMappings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema3.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	n := nodeRow{Node: protocol.Node{Name: "retained-service-node", Admission: "approved"}, KeyHash: hash("retained-key")}
	if err := s.insertNode(&n); err != nil {
		t.Fatal(err)
	}
	if err := s.insertCommand(protocol.Command{ID: "retained-command", NodeID: n.ID, Command: "echo retained", Status: "succeeded", Stdout: []byte("retained")}); err != nil {
		t.Fatal(err)
	}
	f := Forward{NodeID: n.ID, Name: "old-name-not-in-schema3", Protocol: "udp", ListenAddr: "127.0.0.1:32222", TargetHost: "127.0.0.1", TargetPort: 53}
	if err := s.insertForward(&f); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE forwards DROP COLUMN name; PRAGMA user_version=3;`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.forward(f.ID)
	if err != nil || got.Name != f.ListenAddr || got.ID != f.ID || got.Protocol != f.Protocol || !got.CreatedAt.Equal(f.CreatedAt) {
		t.Fatal(got, err)
	}
	retained, err := s.node(n.ID)
	if err != nil || retained.KeyHash != n.KeyHash {
		t.Fatal(retained, err)
	}
	command, err := s.command("retained-command")
	if err != nil || string(command.Stdout) != "retained" {
		t.Fatal(command, err)
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 5 {
		t.Fatal(version, err)
	}
	got.Name = "renamed persistent service"
	if err := s.updateForward(got); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err = s.forward(f.ID)
	if err != nil || got.Name != "renamed persistent service" {
		t.Fatal(got, err)
	}
}
