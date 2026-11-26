package server_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"probex/internal/files"
	"probex/internal/protocol"
	"probex/internal/server"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestDeleteNodeCleansConcurrentWorkspace(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir := t.TempDir()
	commandPID := filepath.Join(dir, "command.pid")
	command := f.command(node.ID, "echo $$ > '"+commandPID+"'; exec sleep 60", 90)
	var terminals []*websocket.Conn
	pidFiles := []string{commandPID}
	for i := 0; i < protocol.MaxTerminals; i++ {
		conn, _, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http://", "ws://", 1)+"/api/nodes/"+node.ID+"/terminal", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + f.admin}}})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.CloseNow()
		terminals = append(terminals, conn)
		pidFile := filepath.Join(dir, fmt.Sprintf("shell-%d.pid", i))
		pidFiles = append(pidFiles, pidFile)
		if err := wsjson.Write(ctx, conn, protocol.Message{Type: "input", Data: []byte("echo $$ > '" + pidFile + "'; exec sleep 60\n")}); err != nil {
			t.Fatal(err)
		}
	}
	var pids []int
	for _, file := range pidFiles {
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
				t.Fatal("workspace process did not start", file, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	owner := openFileOwner(t, f)
	destination := filepath.Join(dir, "preserved.txt")
	if err := os.WriteFile(destination, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	var upload files.Upload
	jsonFileRequest(t, f, "POST", "/api/nodes/"+node.ID+"/uploads", owner.id, files.UploadRequest{Path: destination, Size: 4 << 20, Overwrite: true}, 201, &upload)
	response := fileRequest(t, f, "PATCH", "/api/uploads/"+upload.ID, owner.id, strings.NewReader(strings.Repeat("x", 1<<20)), http.Header{"Upload-Offset": []string{"0"}}, 200)
	response.Body.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "x")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer target.Close()
	address := freeForwardAddress(t)
	var mapping server.Forward
	f.request("POST", "/api/forwards", forwardBody(node.ID, address, strings.TrimPrefix(target.URL, "http://")), 201, &mapping)
	client := &http.Client{Timeout: 10 * time.Second}
	forwarded, err := client.Get("http://" + address + "/active")
	if err != nil {
		t.Fatal(err)
	}
	defer forwarded.Body.Close()
	first := make([]byte, 1)
	if _, err := io.ReadFull(forwarded.Body, first); err != nil || string(first) != "x" {
		t.Fatal(first, err)
	}
	udpAddress := freeForwardAddress(t)
	udpBody := forwardBody(node.ID, udpAddress, "127.0.0.1:9")
	udpBody["protocol"] = "udp"
	f.request("POST", "/api/forwards", udpBody, 201, nil)
	f.request("DELETE", "/api/nodes/"+node.ID, nil, 200, nil)
	select {
	case err := <-f.done:
		if err == nil || !strings.Contains(err.Error(), "revoked") {
			t.Fatal("Agent revocation", err)
		}
		f.cancel()
		f.cancel = nil
	case <-ctx.Done():
		t.Fatal("deleted Agent retained concurrent resources")
	}
	for _, conn := range terminals {
		for {
			var message protocol.Message
			err := wsjson.Read(ctx, conn, &message)
			if err != nil || message.Type == "term_exit" {
				break
			}
		}
	}
	if _, err := io.ReadAll(forwarded.Body); err == nil {
		t.Fatal("active forwarding stream ended without interruption")
	}
	if _, err := net.DialTimeout("tcp", address, time.Second); err == nil {
		t.Fatal("deleted TCP listener remains")
	}
	listener, err := net.ListenPacket("udp", udpAddress)
	if err != nil {
		t.Fatal("deleted UDP listener remains", err)
	}
	listener.Close()
	noUploadTemps(t, dir)
	if data, err := os.ReadFile(destination); err != nil || string(data) != "original" {
		t.Fatal("partial overwrite changed original", string(data), err)
	}
	for _, pid := range pids {
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Fatal("workspace process remains", pid, err)
		}
	}
	f.request("GET", "/api/nodes/"+node.ID, nil, 404, nil)
	f.request("GET", "/api/commands/"+command.ID, nil, 404, nil)
	var mappings []server.Forward
	f.request("GET", "/api/forwards", nil, 200, &mappings)
	if len(mappings) != 0 {
		t.Fatal("mapping configuration recreated", mappings)
	}
	var nodes []protocol.Node
	f.request("GET", "/api/nodes", nil, 200, &nodes)
	if len(nodes) != 0 {
		t.Fatal("late report recreated node", nodes)
	}
}
