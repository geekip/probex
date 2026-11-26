package server_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"probex/internal/files"
	"probex/internal/protocol"
	"probex/internal/server"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestBuiltInNodeWorkspace(t *testing.T) {
	f := newFixture(t, false)
	f.request("POST", "/api/nodes", map[string]string{"name": "AAA"}, 201, nil)
	f.request("POST", "/api/nodes", map[string]string{"name": "Probexd-Local"}, 201, nil)
	restart := func(enabled bool) {
		f.app.Close()
		f.http.Close()
		app, err := server.New(f.store, server.Config{AdminToken: f.admin, LocalNode: enabled})
		if err != nil {
			t.Fatal(err)
		}
		f.app = app
		f.http = httptest.NewServer(app.Handler())
	}
	restart(true)
	var nodes []protocol.Node
	f.request("GET", "/api/nodes", nil, 200, &nodes)
	if len(nodes) != 3 || nodes[0].ID != protocol.LocalNodeID || nodes[0].Name != "Probexd-Local" ||
		nodes[0].JoinMode != "local" || !nodes[0].Online || nodes[0].Info.OS == "" || nodes[0].LastSeen.IsZero() || nodes[1].Name != "AAA" {
		t.Fatal(nodes)
	}
	for path, status := range map[string]int{"/agent": 401, "/agent/data": 403, "/registration": 401, "/api/nodes/0": 401} {
		req, err := http.NewRequest("GET", f.http.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Probex-ID", protocol.LocalNodeID)
		req.Header.Set("Authorization", "Bearer "+protocol.RandomID())
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != status {
			t.Fatalf("%s returned %d, want %d", path, res.StatusCode, status)
		}
	}
	const base = "/api/nodes/" + protocol.LocalNodeID
	f.request("DELETE", base, nil, 403, nil)
	f.request("DELETE", "/api/nodes/00", nil, 404, nil)
	f.request("POST", "/api/nodes/00/enrollment", nil, 404, nil)
	f.request("POST", base+"/enrollment", nil, 403, nil)
	f.request("PATCH", base, map[string]string{"name": "Z-Renamed"}, 200, nil)

	t.Run("terminal", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		conn, _, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http://", "ws://", 1)+base+"/terminal",
			&websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + f.admin}}})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.CloseNow()
		for _, message := range []protocol.Message{
			{Type: "resize", Cols: 100, Rows: 40},
			{Type: "input", Data: []byte("printf '%s%s\\n' LOCAL- PTY; stty size; exit 7\n")},
		} {
			if err := wsjson.Write(ctx, conn, message); err != nil {
				t.Fatal(err)
			}
		}
		var output strings.Builder
		for {
			var message protocol.Message
			if err := wsjson.Read(ctx, conn, &message); err != nil {
				t.Fatal(err, output.String())
			}
			output.Write(message.Data)
			if message.Type == "term_exit" {
				if message.ExitCode == nil || *message.ExitCode != 7 || !strings.Contains(output.String(), "LOCAL-PTY") {
					t.Fatal(message, output.String())
				}
				break
			}
		}
	})
	t.Run("files", func(t *testing.T) {
		owner := openFileOwner(t, f)
		dir := t.TempDir()
		file := filepath.Join(dir, "local.txt")
		payload := bytes.Repeat([]byte("local file\n"), 32768)
		var upload files.Upload
		jsonFileRequest(t, f, "POST", base+"/uploads", owner.id, files.UploadRequest{Path: file, Size: int64(len(payload))}, 201, &upload)
		uploadPath := "/api/uploads/" + upload.ID
		patch := fileRequest(t, f, "PATCH", uploadPath, owner.id, bytes.NewReader(payload), http.Header{"Upload-Offset": {"0"}}, 200)
		patch.Body.Close()
		jsonFileRequest(t, f, "POST", uploadPath+"/commit", owner.id, nil, 200, &upload)
		if upload.State != "completed" {
			t.Fatal(upload)
		}
		var list files.Listing
		jsonFileRequest(t, f, "GET", base+"/files?path="+url.QueryEscape(dir), owner.id, nil, 200, &list)
		if len(list.Entries) != 1 || list.Entries[0].Name != "local.txt" {
			t.Fatal(list)
		}
		content := fileRequest(t, f, "GET", base+"/file?path="+url.QueryEscape(file), owner.id, nil, nil, 200)
		defer content.Body.Close()
		got, err := io.ReadAll(content.Body)
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatal("invalid download", len(got), err)
		}
	})

	address := freeForwardAddress(t)
	body := forwardBody(protocol.LocalNodeID, address, dualEchoTarget(t))
	body["protocol"] = "tcp+udp"
	var forward server.Forward
	f.request("POST", "/api/forwards", body, 201, &forward)
	if forward.NodeID != protocol.LocalNodeID || forward.Status != "ready" {
		t.Fatal(forward)
	}
	tcpEchoMessage(t, tcpEchoConnection(t, address), "local TCP")
	got, err := udpExchange(udpClient(t, address), []byte("local UDP"))
	if err != nil || string(got) != "local UDP" {
		t.Fatal(string(got), err)
	}

	restart(false)
	f.request("GET", "/api/nodes", nil, 200, &nodes)
	if len(nodes) != 2 || nodes[0].ID == protocol.LocalNodeID {
		t.Fatal(nodes)
	}
	f.request("GET", base, nil, 404, nil)
	f.request("POST", "/api/forwards", body, 404, nil)
	f.request("PUT", "/api/forwards/"+forward.ID, body, 404, nil)
	var mappings []server.Forward
	f.request("GET", "/api/forwards", nil, 200, &mappings)
	if len(mappings) != 0 {
		t.Fatal(mappings)
	}
	bindTCP, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("disabled built-in node still listens on TCP", err)
	}
	bindTCP.Close()
	bindUDP, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Fatal("disabled built-in node still listens on UDP", err)
	}
	bindUDP.Close()
	f.startAgent()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.request("GET", "/api/nodes", nil, 200, &nodes)
		if slices.ContainsFunc(nodes, func(n protocol.Node) bool { return n.Name == "test-node" && n.Online }) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("remote agent unavailable while built-in node is disabled", nodes)
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.stopAgent()

	restart(true)
	f.request("GET", "/api/nodes", nil, 200, &nodes)
	if len(nodes) != 4 || nodes[0].ID != protocol.LocalNodeID || nodes[0].Name != "Z-Renamed" || !nodes[0].Online {
		t.Fatal(nodes)
	}
	f.request("GET", "/api/forwards?node_id=0", nil, 200, &mappings)
	if len(mappings) != 1 || mappings[0].ID != forward.ID || mappings[0].Status != "ready" || mappings[0].NodeName != "Z-Renamed" {
		t.Fatal(mappings)
	}
	tcpEchoMessage(t, tcpEchoConnection(t, address), "local TCP after restart")
}
