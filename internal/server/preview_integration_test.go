package server_test

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilePreviewAgentAuthenticationRangeAndSafeTypes(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	root := t.TempDir()
	for _, c := range []struct {
		name, mime string
		data       []byte
	}{
		{"image.png", "image/png", []byte{137, 80, 78, 71, 13, 10, 26, 10, 1, 2, 3, 4}},
		{"vector.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>document.body.textContent='never execute'</script></svg>`)},
		{"movie.mp4", "video/mp4", []byte("....ftypisom0123456789")},
		{"audio.mp3", "audio/mpeg", []byte("ID3.....audio data")},
		{"document.pdf", "application/pdf", []byte("%PDF-1.7\n...file data")},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(root, c.name)
			if err := os.WriteFile(path, c.data, 0600); err != nil {
				t.Fatal(err)
			}
			endpoint := "/api/nodes/" + node.ID + "/preview?path=" + url.QueryEscape(path)
			jsonFileRequest(t, f, "GET", endpoint, "", nil, 410, nil)
			response := fileRequest(t, f, "GET", endpoint, owner.id, nil, http.Header{"Range": []string{"bytes=4-9"}}, 206)
			if response.Header.Get("Content-Type") != c.mime || response.Header.Get("Content-Disposition") != "inline" || response.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(response.Header.Get("Content-Security-Policy"), "sandbox") || !strings.Contains(response.Header.Get("Content-Security-Policy"), "default-src 'none'") {
				t.Fatal(response.Header)
			}
			data, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || !bytes.Equal(data, c.data[4:10]) {
				t.Fatal(data, err)
			}
			response = fileRequest(t, f, "HEAD", endpoint, owner.id, nil, nil, 200)
			response.Body.Close()
			if response.ContentLength != int64(len(c.data)) || response.Header.Get("Accept-Ranges") != "bytes" {
				t.Fatal(response.Header)
			}
		})
	}
	for _, name := range []string{"unsafe.html", "script.js", "unknown.bin"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("<script>never inline</script>"), 0600); err != nil {
			t.Fatal(err)
		}
		jsonFileRequest(t, f, "GET", "/api/nodes/"+node.ID+"/preview?path="+url.QueryEscape(path), owner.id, nil, 415, nil)
	}
	// Media streams are independent of the 10MiB text cap and retain 64-bit ranges.
	path := filepath.Join(root, "large.mp4")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	const size = 5<<30 + 17
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("tail"), size-4); err != nil {
		t.Fatal(err)
	}
	file.Close()
	response := fileRequest(t, f, "GET", "/api/nodes/"+node.ID+"/preview?path="+url.QueryEscape(path), owner.id, nil, http.Header{"Range": []string{"bytes=-4"}}, 206)
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(data) != "tail" {
		t.Fatal(data, err)
	}
}
