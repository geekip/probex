package server_test

import (
	"bytes"
	"context"
	"errors"
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
	write(protocol.Message{Type: "input", Data: []byte("/bin/sh -c 'printf \"\\137START\\137\\n\"; exec sleep 30'\n")})
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

func TestEightIndependentTerminalsWithoutDirectoryTelemetry(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	endpoint := strings.Replace(f.http.URL, "http://", "ws://", 1) + "/api/nodes/" + node.ID + "/terminal"
	// Separate logins still share the node's eight-shell budget.
	var connections []*websocket.Conn
	for i := 0; i < protocol.MaxTerminals; i++ {
		login, err := http.Post(f.http.URL+"/api/login", "application/json", strings.NewReader(`{"token":"`+f.admin+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		login.Body.Close()
		cookie := login.Cookies()[0]
		conn, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: http.Header{"Cookie": []string{cookie.Name + "=" + cookie.Value}}})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.CloseNow()
		connections = append(connections, conn)
	}
	extra, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}}})
	if err == nil {
		extra.CloseNow()
		t.Fatal("ninth shell accepted")
	}
	if response == nil || response.StatusCode != 409 {
		t.Fatal("wrong limit response", err)
	}
	for i, conn := range connections {
		directory := filepath.Join(t.TempDir(), "中文 space "+strconv.Itoa(i))
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := wsjson.Write(ctx, conn, protocol.Message{Type: "resize", Cols: 90 + i, Rows: 30 + i}); err != nil {
			t.Fatal(err)
		}
		command := "cd '" + directory + "'; export PROBEX_SESSION=" + strconv.Itoa(i) + "; stty size; printf '\\137PWD\\137%s\\n' \"$PWD\"; printf '\\033]9;9;/pass-through\\007'; printf '\\137SESSION\\137%s\\n' \"$PROBEX_SESSION\"\n"
		if err := wsjson.Write(ctx, conn, protocol.Message{Type: "input", Data: []byte(command)}); err != nil {
			t.Fatal(err)
		}
		output := ""
		for !strings.Contains(output, "\x1b]9;9;/pass-through\x07") || !strings.Contains(output, "_PWD_"+directory) || !strings.Contains(output, strconv.Itoa(30+i)+" "+strconv.Itoa(90+i)) || !strings.Contains(output, "_SESSION_"+strconv.Itoa(i)) {
			var m protocol.Message
			if err := wsjson.Read(ctx, conn, &m); err != nil {
				t.Fatalf("shell %d output=%q: %v", i, output, err)
			}
			if m.Type == "term_cwd" {
				t.Fatal("terminal emitted unsolicited directory telemetry", m)
			}
			output += string(m.Data)
		}
	}
	parallel := f.wait(f.command(node.ID, "printf parallel-eight-shells", 5))
	if parallel.Status != "succeeded" || string(parallel.Stdout) != "parallel-eight-shells" {
		t.Fatal(parallel)
	}
	if err := wsjson.Write(ctx, connections[0], protocol.Message{Type: "close"}); err != nil {
		t.Fatal(err)
	}
	for {
		var m protocol.Message
		if err := wsjson.Read(ctx, connections[0], &m); err != nil {
			t.Fatal(err)
		}
		if m.Type == "term_exit" {
			break
		}
	}
	fresh, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}}})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.CloseNow()
	if err := wsjson.Write(ctx, fresh, protocol.Message{Type: "input", Data: []byte("printf '\\137FRESH\\137%s\\n' \"${PROBEX_SESSION-unset}\"\n")}); err != nil {
		t.Fatal(err)
	}
	output := ""
	for !strings.Contains(output, "_FRESH_unset") {
		var m protocol.Message
		if err := wsjson.Read(ctx, fresh, &m); err != nil {
			t.Fatal(err)
		}
		output += string(m.Data)
	}
	for i, conn := range connections[1:] {
		if err := wsjson.Write(ctx, conn, protocol.Message{Type: "input", Data: []byte("printf '\\137KEPT\\137%s\\n' \"$PROBEX_SESSION\"\n")}); err != nil {
			t.Fatal(err)
		}
		output := ""
		for !strings.Contains(output, "_KEPT_"+strconv.Itoa(i+1)) {
			var m protocol.Message
			if err := wsjson.Read(ctx, conn, &m); err != nil {
				t.Fatal(err)
			}
			output += string(m.Data)
		}
	}
	var history []protocol.Command
	f.request("GET", "/api/nodes/"+node.ID+"/commands", nil, 200, &history)
	if len(history) != 1 || history[0].ID != parallel.ID {
		t.Fatal("terminal history persisted", history)
	}
}

func TestTerminalDrainsLargeOutputBeforeExit(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http://", "ws://", 1)+"/api/nodes/"+node.ID+"/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := wsjson.Write(ctx, conn, protocol.Message{Type: "input", Data: []byte("stty -echo; head -c 2097152 /dev/zero | tr '\\000' X; printf '\\137TAIL\\137'; exit\n")}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	for {
		var m protocol.Message
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			t.Fatal(err)
		}
		output.Write(m.Data)
		if m.Type == "term_exit" {
			break
		}
	}
	want := append(bytes.Repeat([]byte{'X'}, 2097152), "_TAIL_"...)
	if !bytes.Contains(output.Bytes(), want) {
		t.Fatalf("truncated terminal output length=%d suffix=%q", output.Len(), output.Bytes()[max(0, output.Len()-100):])
	}
}

func TestTerminalReclaimsForegroundAndBackgroundJobs(t *testing.T) {
	for _, action := range []string{"close", "revoke", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t, false)
			f.startAgent()
			node := f.node()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http://", "ws://", 1)+"/api/nodes/"+node.ID+"/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}}})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			dir := t.TempDir()
			bg, fg := filepath.Join(dir, "background.pid"), filepath.Join(dir, "foreground.pid")
			command := "sleep 60 & echo $! > '" + bg + "'; /bin/sh -c 'echo $$ > \"" + fg + "\"; exec sleep 60'\n"
			if err := wsjson.Write(ctx, conn, protocol.Message{Type: "input", Data: []byte(command)}); err != nil {
				t.Fatal(err)
			}
			var pids []int
			for _, file := range []string{bg, fg} {
				deadline := time.Now().Add(3 * time.Second)
				for {
					data, err := os.ReadFile(file)
					if err == nil {
						pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
						if err != nil {
							t.Fatal(err)
						}
						pids = append(pids, pid)
						break
					}
					if !os.IsNotExist(err) || time.Now().After(deadline) {
						t.Fatal("job did not start", err)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			switch action {
			case "close":
				if err := wsjson.Write(ctx, conn, protocol.Message{Type: "close"}); err != nil {
					t.Fatal(err)
				}
			case "revoke":
				f.request("DELETE", "/api/nodes/"+node.ID, nil, 200, nil)
				select {
				case err := <-f.done:
					if err == nil {
						t.Fatal("revocation must stop the Agent")
					}
					f.cancel()
					f.cancel = nil
				case <-ctx.Done():
					t.Fatal("revoked Agent retained jobs")
				}
			case "disconnect":
				f.stopAgent()
			}
			for {
				var m protocol.Message
				err := wsjson.Read(ctx, conn, &m)
				if m.Type == "term_exit" || err != nil {
					break
				}
			}
			for _, pid := range pids {
				deadline := time.Now().Add(2 * time.Second)
				for {
					data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
					// Container PID 1 may retain a reparented zombie; it owns no running job.
					if os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) || err == nil && strings.Fields(string(data))[2] == "Z" {
						break
					}
					if err != nil || time.Now().After(deadline) {
						t.Fatalf("%s retained job %d: %v %q", action, pid, err, data)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
		})
	}
}
