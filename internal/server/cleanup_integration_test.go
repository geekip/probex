package server_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"probex/internal/protocol"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestClearHistoryPreservesTerminalAndOtherNodes(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	first := f.wait(f.command(node.ID, "printf HISTORY", 5))
	var other struct {
		Node protocol.Node `json:"node"`
	}
	f.request("POST", "/api/nodes", map[string]string{"name": "other"}, 201, &other)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http://", "ws://", 1)+"/api/nodes/"+node.ID+"/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	f.request("DELETE", "/api/nodes/"+node.ID+"/commands", nil, 200, nil)
	var records []protocol.Command
	f.request("GET", "/api/nodes/"+node.ID+"/commands", nil, 200, &records)
	if len(records) != 0 {
		t.Fatal("history not cleared", records)
	}
	f.request("GET", "/api/commands/"+first.ID, nil, 404, nil)
	f.request("GET", "/api/nodes/"+other.Node.ID, nil, 200, nil)
	if err := wsjson.Write(ctx, conn, protocol.Message{Type: "input", Data: []byte("printf '\\137AFTER_CLEAR\\137\\n'\n")}); err != nil {
		t.Fatal(err)
	}
	output := ""
	for !strings.Contains(output, "_AFTER_CLEAR_") {
		var message protocol.Message
		if err := wsjson.Read(ctx, conn, &message); err != nil {
			t.Fatal(err)
		}
		output += string(message.Data)
	}
	command := f.command(node.ID, "sleep 1; printf AFTER_CLEAR", 5)
	f.request("DELETE", "/api/nodes/"+node.ID+"/commands", nil, 200, &records)
	if len(records) != 1 || records[0].ID != command.ID {
		t.Fatal("running command was cleared", records)
	}
	command = f.wait(command)
	if command.Status != "succeeded" || string(command.Stdout) != "AFTER_CLEAR" {
		t.Fatal(command)
	}
	f.request("GET", "/api/nodes/"+node.ID+"/commands", nil, 200, &records)
	if len(records) != 1 || records[0].ID != command.ID {
		t.Fatal("terminal recorded or cleared records recreated", records)
	}
	f.request("DELETE", "/api/nodes/"+node.ID+"/commands", nil, 200, nil)
	f.request("GET", "/api/commands/"+command.ID, nil, 404, nil)
}

func TestDeletedPendingIdentityCannotResubmit(t *testing.T) {
	f := newFixture(t, true)
	key := protocol.RandomID()
	pending := candidate(f, "POST", "", key, "pending", 202)
	f.request("DELETE", "/api/nodes/"+pending.ID, nil, 200, nil)
	candidate(f, "POST", pending.ID, key, "pending", 403)
	candidate(f, "GET", pending.ID, key, "", 403)
	var nodes []protocol.Node
	f.request("GET", "/api/nodes", nil, 200, &nodes)
	if len(nodes) != 0 {
		t.Fatal("deleted profile recreated node", nodes)
	}
	next := candidate(f, "POST", "", protocol.RandomID(), "pending", 202)
	if next.ID == pending.ID {
		t.Fatal("deleted ID reused")
	}
}

func TestDeleteActiveCommandStopsProcessAndRemovesHistory(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	file := filepath.Join(t.TempDir(), "command.pid")
	command := f.command(node.ID, "echo $$ > "+file+"; exec sleep 30", 60)
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(file); err == nil {
			pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("command did not start")
	}
	f.request("DELETE", "/api/nodes/"+node.ID, nil, 200, nil)
	f.request("GET", "/api/nodes/"+node.ID, nil, 404, nil)
	f.request("GET", "/api/commands/"+command.ID, nil, 404, nil)
	select {
	case err := <-f.done:
		if err == nil {
			t.Fatal("deleted agent did not report revocation")
		}
		f.cancel()
		f.cancel = nil
	case <-time.After(5 * time.Second):
		t.Fatal("deleted agent did not stop")
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			f.request("GET", "/api/commands/"+command.ID, nil, 404, nil)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("command process survived node deletion")
}
