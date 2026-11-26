package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"probex/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestFileResponseHeadersAreScoped(t *testing.T) {
	src := http.Header{
		"Content-Length": {"10"},
		"Content-Range":  {"bytes 0-9/20"},
		"Content-Type":   {"text/plain"},
		"Cache-Control":  {"public"},
		"Vary":           {"Accept"},
	}
	dst := http.Header{"Cache-Control": {"no-store"}}
	copyResponseHeaders(dst, src, "Content-Length", "Content-Range")
	if len(dst) != 3 || dst.Get("Content-Length") != "10" || dst.Get("Content-Range") != "bytes 0-9/20" || dst.Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected headers: %v", dst)
	}
}

func TestFileRequestHeadersAreScoped(t *testing.T) {
	src := http.Header{
		"X-Custom-Identity": {"authenticated-user"},
		"Cookie":            {"session=example"},
		"Content-Type":      {"application/octet-stream"},
		"Range":             {"bytes=0-5"},
		"If-Range":          {"example"},
		"Upload-Offset":     {"6"},
	}
	dst := make(http.Header)
	copyFileRequestHeaders(dst, src)
	if len(dst) != 4 || dst.Get("Content-Type") != src.Get("Content-Type") ||
		dst.Get("Range") != src.Get("Range") || dst.Get("If-Range") != src.Get("If-Range") ||
		dst.Get("Upload-Offset") != src.Get("Upload-Offset") {
		t.Fatalf("unexpected headers: %v", dst)
	}
}

func TestCommandSummaryBackfillAndUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	node := nodeRow{Node: protocol.Node{Name: "history", Admission: "approved"}}
	if err := s.insertNode(&node); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP TRIGGER command_summary_insert; DROP TRIGGER command_summary_update; DROP TABLE command_summaries"); err != nil {
		t.Fatal(err)
	}
	older := protocol.Command{ID: "1", NodeID: node.ID, Status: "finished", CreatedAt: time.Unix(100, 0).UTC(), Stdout: []byte{0, 128, 255}}
	newer := protocol.Command{ID: "2", NodeID: node.ID, Status: "running", CreatedAt: time.Unix(101, 0).UTC(), Stderr: []byte("stderr")}
	for _, command := range []protocol.Command{older, newer} {
		if err := s.insertCommand(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.updateCommand(older.ID, func(c *protocol.Command) { c.Truncated = true }); err != nil {
		t.Fatal(err)
	}
	all, err := s.history(node.ID, 0)
	if err != nil || len(all) != 2 || all[0].ID != newer.ID || all[0].Status != "unknown" || all[0].Stderr != nil ||
		all[1].ID != older.ID || all[1].Stdout != nil || !all[1].Truncated {
		t.Fatalf("unexpected summaries: %+v %v", all, err)
	}
	detail, err := s.command(older.ID)
	if err != nil || string(detail.Stdout) != string(older.Stdout) {
		t.Fatalf("legacy output unavailable: %v %v", detail.Stdout, err)
	}
	if err := s.clearHistory(node.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM command_summaries").Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphaned summaries: %d (%v)", count, err)
	}
}

func TestCopyDirectoryNameUsesResolvedSource(t *testing.T) {
	work := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(filepath.Join(work, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(work, "child"))
	c := copyClient{node: protocol.Node{Info: protocol.Info{OS: "linux"}}}
	source, exists, err := c.stat("..", false)
	if err != nil || !exists || source.Name != "source" || source.Path != work {
		t.Fatalf("failed to resolve source: %+v, %v, %v", source, exists, err)
	}
	got, err := c.join("/backup", source.Name, true)
	if err != nil || got != "/backup/source" {
		t.Fatal(got, err)
	}
	for _, name := range []string{"", ".", "..", "dir/child"} {
		for _, remote := range []bool{false, true} {
			if _, err := c.join("/backup", name, remote); err == nil {
				t.Fatalf("invalid segment %q accepted", name)
			}
		}
	}
	c.node.Info.OS = "windows"
	if _, err := c.join(`C:\backup`, `dir\child`, true); err == nil {
		t.Fatal("Windows path separator accepted in a basename")
	}
}

func TestExportSelfSignedServerCertificateChecksTimeAndUsage(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, test := range []struct {
		name  string
		end   time.Time
		usage x509.ExtKeyUsage
		ok    bool
	}{
		{"valid server", now.Add(time.Hour), x509.ExtKeyUsageServerAuth, true},
		{"expired server", now.Add(-time.Minute), x509.ExtKeyUsageServerAuth, false},
		{"client only", now.Add(time.Hour), x509.ExtKeyUsageClientAuth, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cert := &x509.Certificate{
				SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: test.end,
				ExtKeyUsage: []x509.ExtKeyUsage{test.usage},
			}
			der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, priv)
			if err != nil {
				t.Fatal(err)
			}
			_, err = exportCA([][]byte{der})
			if (err == nil) != test.ok {
				t.Fatalf("unexpected verification result: %v", err)
			}
		})
	}
}

func TestCLINoReplaceKeepsConcurrentDownload(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Query().Get("path")))
	}))
	defer remote.Close()
	dest := filepath.Join(t.TempDir(), "saved.txt")
	c := copyClient{ctx: context.Background(), base: remote.URL, client: remote.Client(), node: protocol.Node{ID: "1"}}
	start := make(chan struct{})
	type copyResult struct {
		path string
		err  error
	}
	done := make(chan copyResult, 2)
	for _, path := range []string{"/first", "/second"} {
		go func() {
			<-start
			done <- copyResult{path, c.download(path, dest, false)}
		}()
	}
	close(start)
	a, b := <-done, <-done
	if a.err != nil {
		a, b = b, a
	}
	data, err := os.ReadFile(dest)
	if a.err != nil || b.err == nil || err != nil || string(data) != a.path {
		t.Fatalf("concurrent downloads: %+v %+v, saved %q (%v)", a, b, data, err)
	}
}

func TestManagementPortSurvivesConflictingSavedForward(t *testing.T) {
	const childAddress = "PROBEX_TEST_MANAGEMENT_ADDRESS"
	const childDB = "PROBEX_TEST_MANAGEMENT_DB"
	if address := os.Getenv(childAddress); address != "" {
		if err := Main([]string{"listen", address, "--admin-token", "test", "--db", os.Getenv(childDB)}); err != nil {
			t.Fatal(err)
		}
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	db := filepath.Join(t.TempDir(), "forwards.db")
	store, err := OpenStore(db)
	if err != nil {
		t.Fatal(err)
	}
	node := nodeRow{Node: protocol.Node{Name: "test-node", Admission: "approved"}}
	if err := store.insertNode(&node); err != nil {
		t.Fatal(err)
	}
	if err := store.insertForward(&Forward{NodeID: node.ID, Protocol: "tcp", ListenAddr: address, TargetHost: "127.0.0.1", TargetPort: 22}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestManagementPortSurvivesConflictingSavedForward$")
	cmd.Env = append(os.Environ(), childAddress+"="+address, childDB+"="+db)
	output := make(chan string, 1)
	go func() { data, _ := cmd.CombinedOutput(); output <- string(data) }()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	client := http.Client{Timeout: 200 * time.Millisecond}
	for {
		select {
		case data := <-output:
			t.Fatalf("server exited before binding management: %s", data)
		case <-ctx.Done():
			t.Fatal("management server did not respond")
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, "GET", "http://"+address+"/api/forwards", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer test")
			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 200 || !strings.Contains(string(body), `"status":"listen_error"`) {
				t.Fatalf("unexpected saved forward response: %d, %s (%v)", resp.StatusCode, body, err)
			}
			cancel()
			if data := <-output; !strings.Contains(data, address+": listen port unavailable:") {
				t.Fatalf("startup did not log unavailable forwarding port: %s", data)
			}
			return
		}
	}
}
