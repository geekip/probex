package server_test

import (
	"bytes"
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

func TestInteractiveTerminal(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http://", "ws://", 1)+"/api/nodes/"+node.ID+"/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}, "Origin": []string{f.http.URL}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	write := func(m protocol.Message) {
		t.Helper()
		if err := wsjson.Write(ctx, conn, m); err != nil {
			t.Fatal(err)
		}
	}
	output := ""
	wait := func(text string) {
		t.Helper()
		for !strings.Contains(output, text) {
			var m protocol.Message
			if err := wsjson.Read(ctx, conn, &m); err != nil {
				t.Fatalf("waiting for %q: %v; output=%q", text, err, output)
			}
			if m.Type == "term_exit" {
				t.Fatalf("terminal exited early: %+v", m)
			}
			output += string(m.Data)
		}
	}
	write(protocol.Message{Type: "input", Data: []byte("cd /tmp\n")})
	write(protocol.Message{Type: "input", Data: []byte("printf '\\137PWD\\137%s\\n' \"$PWD\"\n")})
	wait("_PWD_/tmp")
	write(protocol.Message{Type: "resize", Cols: 100, Rows: 40})
	write(protocol.Message{Type: "input", Data: []byte("stty size\n")})
	wait("40 100")
	parallel := f.wait(f.command(node.ID, "printf parallel-command", 5))
	if parallel.Status != "succeeded" || string(parallel.Stdout) != "parallel-command" {
		t.Fatalf("command during terminal failed: %+v", parallel)
	}
	write(protocol.Message{Type: "input", Data: []byte("printf '\\137START\\137\\n'; sleep 30\n")})
	wait("_START_")
	write(protocol.Message{Type: "input", Data: []byte{3}})
	write(protocol.Message{Type: "input", Data: []byte("printf '\\137AFTER\\137\\n'\n")})
	wait("_AFTER_")
	write(protocol.Message{Type: "input", Data: []byte("top\n")})
	wait("Tasks:")
	write(protocol.Message{Type: "input", Data: []byte("q")})
	wait("\x1b[?25h")
	write(protocol.Message{Type: "input", Data: []byte("\nprintf '\\137TOPDONE\\137\\n'\n")})
	wait("_TOPDONE_")
	write(protocol.Message{Type: "input", Data: []byte("exit\n")})
	for {
		var m protocol.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatal(err)
		}
		if m.Type == "term_exit" {
			if m.ExitCode == nil || *m.ExitCode != 0 || m.Error != "" {
				t.Fatalf("bad terminal exit: %+v", m)
			}
			break
		}
	}
	var history []protocol.Command
	f.request("GET", "/api/nodes/"+node.ID+"/commands", nil, 200, &history)
	if len(history) != 1 || history[0].ID != parallel.ID {
		t.Fatalf("bad terminal history: %+v", history)
	}
	c := f.wait(f.command(node.ID, "printf after-terminal", 5))
	if c.Status != "succeeded" {
		t.Fatal("terminal slot not released")
	}
}

func TestTerminalCloseAcknowledgedBeforeReload(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for range 2 {
		conn, _, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http://", "ws://", 1)+"/api/nodes/"+node.ID+"/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := wsjson.Write(ctx, conn, protocol.Message{Type: "close"}); err != nil {
			t.Fatal(err)
		}
		for {
			var m protocol.Message
			if err := wsjson.Read(ctx, conn, &m); err != nil {
				t.Fatal(err)
			}
			if m.Type == "term_exit" {
				break
			}
		}
		conn.CloseNow()
	}
	c := f.wait(f.command(node.ID, "printf released", 5))
	if c.Status != "succeeded" {
		t.Fatal("node slot not released after close acknowledgement")
	}
}

func TestTerminalDisconnectReleasesNode(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http://", "ws://", 1)+"/api/nodes/"+node.ID+"/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}}})
	if err != nil {
		t.Fatal(err)
	}
	conn.CloseNow()
	// Reopening verifies release of the terminal slot without relying on persisted history.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		next, response, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http://", "ws://", 1)+"/api/nodes/"+node.ID+"/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}}})
		if err == nil {
			next.CloseNow()
			var history []protocol.Command
			f.request("GET", "/api/nodes/"+node.ID+"/commands", nil, 200, &history)
			if len(history) != 0 {
				t.Fatal("terminal was persisted", history)
			}
			return
		}
		if response == nil || response.StatusCode != 409 {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("terminal slot was not released after browser disconnect")
}

func TestTerminalRejectsCrossOriginAndClosesOnLogout(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	endpoint := strings.Replace(f.http.URL, "http://", "ws://", 1) + "/api/nodes/" + node.ID + "/terminal"
	_, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}, "Origin": []string{"https://evil.example"}}})
	if err == nil || response == nil || response.StatusCode != 403 {
		t.Fatal("cross-origin terminal accepted")
	}
	response, err = http.Post(f.http.URL+"/api/login", "application/json", bytes.NewBufferString(`{"token":"`+f.admin+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(response.Cookies()) != 1 {
		t.Fatal("missing login cookie")
	}
	cookie := response.Cookies()[0]
	header := http.Header{"Cookie": []string{cookie.Name + "=" + cookie.Value}, "Origin": []string{f.http.URL}}
	conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	req, err := http.NewRequest("POST", f.http.URL+"/api/logout", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header = header
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("logout failed")
	}
	for {
		var message protocol.Message
		if err := wsjson.Read(ctx, conn, &message); err != nil {
			if ctx.Err() != nil {
				t.Fatal("terminal stayed open after logout")
			}
			break
		}
	}
}

func TestUnresponsiveBrowserTerminalIsClosed(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http://", "ws://", 1)+"/api/nodes/"+node.ID+"/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	file := filepath.Join(t.TempDir(), "shell.pid")
	if err := wsjson.Write(ctx, conn, protocol.Message{Type: "input", Data: []byte("echo $$ > " + file + "\n")}); err != nil {
		t.Fatal(err)
	}
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
		t.Fatal("shell did not start")
	}
	// Without a reader this client cannot process pings or send pongs.
	deadline = time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			if !f.node().Online {
				t.Fatal("browser failure disconnected the agent")
			}
			var history []protocol.Command
			f.request("GET", "/api/nodes/"+node.ID+"/commands", nil, 200, &history)
			if len(history) != 0 {
				t.Fatal("terminal persisted history", history)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("unresponsive browser retained its terminal process")
}

func TestActiveTerminalStopsOnRevocationAndAgentDisconnect(t *testing.T) {
	for _, mode := range []string{"revocation", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, false)
			f.startAgent()
			node := f.node()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http://", "ws://", 1)+"/api/nodes/"+node.ID+"/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}}})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			file := filepath.Join(t.TempDir(), "shell.pid")
			if err := wsjson.Write(ctx, conn, protocol.Message{Type: "input", Data: []byte("echo $$ > " + file + "\n")}); err != nil {
				t.Fatal(err)
			}
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
				t.Fatal("terminal shell never started")
			}
			if mode == "revocation" {
				f.request("DELETE", "/api/nodes/"+node.ID, nil, 200, nil)
				select {
				case err := <-f.done:
					if err == nil {
						t.Fatal("revocation should be permanent")
					}
					f.cancel()
					f.cancel = nil
				case <-ctx.Done():
					t.Fatal("revoked agent did not stop")
				}
			} else {
				f.stopAgent()
			}
			for {
				var m protocol.Message
				if err := wsjson.Read(ctx, conn, &m); err != nil {
					if ctx.Err() != nil {
						t.Fatal("browser terminal not disconnected")
					}
					break
				}
			}
			deadline = time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if syscall.Kill(pid, 0) == syscall.ESRCH {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("terminal shell %d survived %s", pid, mode)
		})
	}
}
