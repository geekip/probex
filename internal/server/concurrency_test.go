package server_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"probex/internal/protocol"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestIndependentCommandAndTerminal(t *testing.T) {
	for _, commandFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("command-first=%t", commandFirst), func(t *testing.T) {
			f := newFixture(t, false)
			f.startAgent()
			node := f.node()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			endpoint := strings.Replace(f.http.URL, "http://", "ws://", 1) + "/api/nodes/" + node.ID + "/terminal"
			dial := func(status int) *websocket.Conn {
				t.Helper()
				conn, resp, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}}})
				if resp == nil || resp.StatusCode != status || (status == 101 && err != nil) {
					t.Fatalf("terminal dial: status=%v err=%v", resp, err)
				}
				if conn != nil {
					t.Cleanup(func() { conn.CloseNow() })
				}
				return conn
			}
			write := func(conn *websocket.Conn, m protocol.Message) {
				t.Helper()
				if err := wsjson.Write(ctx, conn, m); err != nil {
					t.Fatal(err)
				}
			}
			readUntil := func(conn *websocket.Conn, marker string) {
				t.Helper()
				var output strings.Builder
				for !strings.Contains(output.String(), marker) {
					var m protocol.Message
					if err := wsjson.Read(ctx, conn, &m); err != nil {
						t.Fatal(err)
					}
					if m.Type == "term_exit" {
						t.Fatalf("terminal exited while command running: %+v", m)
					}
					output.Write(m.Data)
				}
				if strings.Contains(output.String(), "COMMAND_STDOUT") || strings.Contains(output.String(), "COMMAND_STDERR") {
					t.Fatal("command output leaked into terminal")
				}
			}
			closeTerminal := func(conn *websocket.Conn) {
				t.Helper()
				write(conn, protocol.Message{Type: "close"})
				for {
					var m protocol.Message
					if err := wsjson.Read(ctx, conn, &m); err != nil {
						t.Fatal(err)
					}
					if m.Type == "term_exit" {
						return
					}
				}
			}
			flag := filepath.Join(t.TempDir(), "release-command")
			text := fmt.Sprintf("printf COMMAND_STDOUT; printf COMMAND_STDERR >&2; while [ ! -e %q ]; do sleep 0.02; done; exit 7", flag)
			var command protocol.Command
			if commandFirst {
				command = f.command(node.ID, text, 10)
			}
			conn := dial(101)
			write(conn, protocol.Message{Type: "input", Data: []byte("printf '\\137TERMINAL_READY\\137\\n'\n")})
			readUntil(conn, "_TERMINAL_READY_")
			if !commandFirst {
				command = f.command(node.ID, text, 10)
			}
			f.request("POST", "/api/nodes/"+node.ID+"/commands", map[string]any{"command": "echo duplicate"}, 409, nil)
			closeTerminal(dial(101))
			closeTerminal(conn)
			f.request("POST", "/api/nodes/"+node.ID+"/commands", map[string]any{"command": "echo still-busy"}, 409, nil)
			conn = dial(101)
			write(conn, protocol.Message{Type: "input", Data: []byte("printf '\\137RELOADED\\137\\n'\n")})
			readUntil(conn, "_RELOADED_")
			if err := os.WriteFile(flag, nil, 0600); err != nil {
				t.Fatal(err)
			}
			command = f.wait(command)
			if command.Status != "failed" || command.ExitCode == nil || *command.ExitCode != 7 || string(command.Stdout) != "COMMAND_STDOUT" || string(command.Stderr) != "COMMAND_STDERR" || command.StartedAt == nil {
				t.Fatalf("independent command result: %+v", command)
			}
			closeTerminal(dial(101))
			for range 5 {
				c := f.wait(f.command(node.ID, "printf next-command", 3))
				if c.Status != "succeeded" || string(c.Stdout) != "next-command" {
					t.Fatalf("command slot not independently reusable: %+v", c)
				}
			}
			c := f.wait(f.command(node.ID, "sleep 30", 1))
			if c.Status != "timed_out" {
				t.Fatalf("command timeout: %+v", c)
			}
			write(conn, protocol.Message{Type: "input", Data: []byte("printf '\\137STILL_ALIVE\\137\\n'\n")})
			readUntil(conn, "_STILL_ALIVE_")
			closeTerminal(conn)
			var history []protocol.Command
			f.request("GET", "/api/nodes/"+node.ID+"/commands", nil, 200, &history)
			for _, item := range history {
				if !protocol.Terminal(item.Status) || item.StartedAt == nil {
					t.Fatalf("unfinished history after independent cleanup: %+v", item)
				}
			}
		})
	}
}

func TestDisconnectPersistsOnlyCommandUnknown(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	command := f.command(node.ID, "sleep 30", 60)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http://", "ws://", 1)+"/api/nodes/"+node.ID+"/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	f.app.Close()
	f.stopAgent()
	command = f.wait(command)
	var history []protocol.Command
	f.request("GET", "/api/nodes/"+node.ID+"/commands", nil, 200, &history)
	if len(history) != 1 || history[0].ID != command.ID || history[0].Status != "unknown" {
		t.Fatalf("only command must be persisted after disconnect: %+v", history)
	}
}

func TestLegacyProtocolCannotEnroll(t *testing.T) {
	f := newFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var created struct {
		Key string `json:"key"`
	}
	f.request("POST", "/api/nodes", map[string]string{"name": "old-client"}, 201, &created)
	join, err := protocol.DecodeEnrollment(created.Key)
	if err != nil {
		t.Fatal(err)
	}
	conn, _, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http://", "ws://", 1)+"/agent", &websocket.DialOptions{HTTPHeader: http.Header{
		"Authorization":      []string{"Bearer " + protocol.RandomID()},
		"X-Probex-ID":        []string{join.NodeID},
		"X-Enrollment-Token": []string{join.Token},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, protocol.Message{Type: "hello", Version: 1, Name: "old-client", Info: &protocol.Info{}}); err != nil {
		t.Fatal(err)
	}
	var message protocol.Message
	err = wsjson.Read(ctx, conn, &message)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation || !strings.Contains(err.Error(), "upgrade server and client together") {
		t.Fatalf("old protocol was not explicitly rejected: %v", err)
	}
	var nodes []protocol.Node
	f.request("GET", "/api/nodes", nil, 200, &nodes)
	if len(nodes) != 1 || nodes[0].Admission != "invited" {
		t.Fatal("incompatible client was enrolled")
	}
}
