package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"probex/internal/files"
	"probex/internal/protocol"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/mattn/go-isatty"
)

type copyPath struct{ node, path string }

func parseCopyPath(raw string, windows bool) (copyPath, error) {
	if raw == "" {
		return copyPath{}, fmt.Errorf("empty copy path")
	}
	colon := strings.IndexByte(raw, ':')
	slash := strings.IndexAny(raw, "/\\")
	if colon < 0 || slash >= 0 && slash < colon || windows && colon == 1 && ((raw[0] >= 'A' && raw[0] <= 'Z') || (raw[0] >= 'a' && raw[0] <= 'z')) {
		return copyPath{path: raw}, nil
	}
	node, p := raw[:colon], raw[colon+1:]
	if !validName(node) || p == "" {
		return copyPath{}, fmt.Errorf("invalid node path %q", raw)
	}
	return copyPath{node: node, path: p}, nil
}
func parseCopyPaths(args []string, windows bool) ([]copyPath, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("usage: probexd cp [-r] [-f] [--server URL] SOURCE... DESTINATION")
	}
	result := make([]copyPath, 0, len(args))
	for _, arg := range args {
		p, err := parseCopyPath(arg, windows)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	to := result[len(result)-1]
	for _, from := range result[:len(result)-1] {
		if (from.node == "") == (to.node == "") {
			return nil, fmt.Errorf("cp supports local to node or node to local only")
		}
	}
	return result, nil
}

type copyHTTPError struct {
	status  int
	message string
}

func (e *copyHTTPError) Error() string { return fmt.Sprintf("server %d: %s", e.status, e.message) }

type copyClient struct {
	ctx                context.Context
	client             *http.Client
	base, token, owner string
	node               protocol.Node
	recursive, force   bool
	input              *bufio.Reader
}

func (c *copyClient) do(method, route string, body io.Reader, length int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(c.ctx, method, c.base+route, body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = length
	req.Header.Set("Authorization", "Bearer "+c.token)
	if c.owner != "" {
		req.Header.Set("X-Probex-File-Session", c.owner)
	}

	return c.response(req)
}
func (c *copyClient) response(req *http.Request) (*http.Response, error) {
	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 300 {
		defer response.Body.Close()
		b, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		if err != nil {
			return nil, err
		}
		return nil, &copyHTTPError{response.StatusCode, strings.TrimSpace(string(b))}
	}
	return response, nil
}
func (c *copyClient) json(method, route string, body any, result any) error {
	var reader io.Reader
	var length int64
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
		length = int64(len(data))
	}
	response, err := c.do(method, route, reader, length)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if result != nil {
		return json.NewDecoder(response.Body).Decode(result)
	}
	_, err = io.Copy(io.Discard, response.Body)
	return err
}
func (c *copyClient) remote(route, p string) string {
	return "/api/nodes/" + c.node.ID + "/" + route + "?path=" + url.QueryEscape(p)
}
func (c *copyClient) stat(p string, remote bool) (files.Entry, bool, error) {
	if remote {
		var entry files.Entry
		err := c.json("GET", c.remote("stat", p), nil, &entry)
		var status *copyHTTPError
		if errors.As(err, &status) && status.status == 404 {
			return files.Entry{}, false, nil
		}
		return entry, err == nil, err
	}
	p, err := filepath.Abs(p)
	if err != nil {
		return files.Entry{}, false, err
	}
	info, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return files.Entry{}, false, nil
	}
	if err != nil {
		return files.Entry{}, false, err
	}
	kind := "special"
	if info.Mode()&os.ModeSymlink != 0 {
		kind = "symlink"
	} else if info.IsDir() {
		kind = "directory"
	} else if info.Mode().IsRegular() {
		kind = "file"
	}
	size := info.Size()
	return files.Entry{Name: info.Name(), Path: p, Type: kind, Size: &size}, true, nil
}
func (c *copyClient) remoteJoin(parent, name string) string {
	if c.node.Info.OS == "windows" {
		return strings.TrimRight(parent, "/\\") + "\\" + name
	}
	return path.Join(parent, name)
}
func (c *copyClient) join(parent, name string, remote bool) (string, error) {
	windows := remote && c.node.Info.OS == "windows" || !remote && os.PathSeparator == '\\'
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") ||
		windows && (strings.ContainsAny(name, "\\:") || strings.TrimRight(name, ". ") != name) ||
		!remote && !filepath.IsLocal(name) {
		return "", fmt.Errorf("invalid destination filename %q", name)
	}
	if remote {
		return c.remoteJoin(parent, name), nil
	}
	return filepath.Join(parent, name), nil
}
func (c *copyClient) confirm(destination string) error {
	if c.force {
		return nil
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("destination %s exists; use -f to overwrite in non-interactive mode", destination)
	}
	fmt.Fprintf(os.Stderr, "Overwrite %s? [y/N] ", destination)
	type answer struct {
		line string
		err  error
	}
	result := make(chan answer, 1)
	go func() { line, err := c.input.ReadString('\n'); result <- answer{line, err} }()
	select {
	case <-c.ctx.Done():
		return c.ctx.Err()
	case answer := <-result:
		if answer.err != nil {
			return answer.err
		}
		if strings.EqualFold(strings.TrimSpace(answer.line), "y") || strings.EqualFold(strings.TrimSpace(answer.line), "yes") {
			return nil
		}
		return fmt.Errorf("overwrite declined: %s", destination)
	}
}
func copyCommand(client *http.Client, base, token string, paths []copyPath, recursive, force bool) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	client.Timeout = 0
	c := &copyClient{ctx: ctx, client: client, base: base, token: token, recursive: recursive, force: force, input: bufio.NewReader(os.Stdin)}
	var nodes []protocol.Node
	if err := c.json("GET", "/api/nodes", nil, &nodes); err != nil {
		return err
	}
	var nodeID string
	for i := range paths {
		if paths[i].node != "" {
			n, err := resolveNode(nodes, paths[i].node)
			if err != nil {
				return err
			}
			if nodeID != "" && nodeID != n.ID {
				return fmt.Errorf("all remote paths must belong to the same node")
			}
			nodeID = n.ID
			c.node = n
			paths[i].node = n.ID
		}
	}
	if !c.node.Online {
		return fmt.Errorf("node %s is offline", c.node.Name)
	}
	u, err := url.Parse(base)
	if err != nil {
		return err
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/file-sessions"
	conn, response, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}}})
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(protocol.MaxMessage)
	var hello fileEvent
	if err := wsjson.Read(ctx, conn, &hello); err != nil {
		return err
	}
	if hello.Type != "session" || hello.ID == "" {
		return fmt.Errorf("invalid file session response")
	}
	c.owner = hello.ID
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer stop()
		for {
			var event fileEvent
			if err := wsjson.Read(ctx, conn, &event); err != nil {
				return
			}
		}
	}()
	defer func() { stop(); conn.CloseNow(); <-done }()
	to := paths[len(paths)-1]
	target, exists, err := c.stat(to.path, to.node != "")
	if err != nil {
		return err
	}
	targetDirectory := exists && target.Type == "directory"
	if targetDirectory {
		to.path = target.Path
	}
	if len(paths) > 2 && !targetDirectory {
		return fmt.Errorf("multiple sources require an existing destination directory")
	}
	windowsTarget := to.node != "" && c.node.Info.OS == "windows" || to.node == "" && os.PathSeparator == '\\'
	if !exists && (strings.HasSuffix(to.path, "/") || windowsTarget && strings.HasSuffix(to.path, "\\")) {
		return fmt.Errorf("destination directory does not exist: %s", to.path)
	}
	var failures []error
	for _, from := range paths[:len(paths)-1] {
		if ctx.Err() != nil {
			failures = append(failures, ctx.Err())
			break
		}
		source, exists, err := c.stat(from.path, from.node != "")
		destination := to.path
		if err == nil && !exists {
			err = fmt.Errorf("source does not exist: %s", from.path)
		}
		if err == nil && targetDirectory {
			// Roots have no basename to append. Use an explicit new destination.
			name := source.Name
			if from.node == "" && filepath.Dir(source.Path) == source.Path {
				name = ""
			} else if from.node != "" && c.node.Info.OS == "windows" {
				normalized := strings.TrimRight(strings.ReplaceAll(source.Path, "\\", "/"), "/")
				if strings.HasSuffix(normalized, ":") || strings.HasPrefix(normalized, "//") && strings.Count(strings.TrimPrefix(normalized, "//"), "/") == 1 {
					name = ""
				}
			}
			destination, err = c.join(destination, name, to.node != "")
		}
		if err == nil {
			err = c.tree(source, source.Path, destination, from.node != "")
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", from.path, err))
		}
	}
	return errors.Join(failures...)
}
func (c *copyClient) tree(source files.Entry, from, to string, download bool) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if source.Type != "file" && source.Type != "directory" {
		return fmt.Errorf("unsupported %s: %s", source.Type, from)
	}
	target, exists, err := c.stat(to, !download)
	if err != nil {
		return err
	}
	if source.Type == "file" {
		if exists && target.Type != "file" {
			return fmt.Errorf("destination is not a regular file: %s", to)
		}
		if exists {
			if err := c.confirm(to); err != nil {
				return err
			}
		}
		overwrite := exists || c.force
		if download {
			return c.download(from, to, overwrite)
		}
		return c.upload(from, to, overwrite)
	}
	if !c.recursive {
		return fmt.Errorf("directory %s requires -r", from)
	}
	if exists && target.Type != "directory" {
		return fmt.Errorf("cannot replace a file with a directory: %s", to)
	}
	if !exists {
		if download {
			err = os.Mkdir(to, 0755)
		} else {
			err = c.json("POST", "/api/nodes/"+c.node.ID+"/directories", map[string]string{"path": to}, nil)
		}
		if err != nil {
			return err
		}
	}
	var failures []error
	copyChild := func(child files.Entry) {
		destination, err := c.join(to, child.Name, !download)
		if err == nil {
			err = c.tree(child, child.Path, destination, download)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", child.Path, err))
		}
	}
	if download {
		cursor := ""
		for {
			var list files.Listing
			route := c.remote("files", from) + "&hidden=true&fields=size&cursor=" + url.QueryEscape(cursor)
			if err := c.json("GET", route, nil, &list); err != nil {
				failures = append(failures, err)
				break
			}
			for _, child := range list.Entries {
				if c.ctx.Err() != nil {
					return c.ctx.Err()
				}
				copyChild(child)
			}
			if list.Next == "" {
				break
			}
			cursor = list.Next
		}
	} else {
		entries, err := os.ReadDir(from)
		if err != nil {
			return err
		}
		for _, d := range entries {
			if c.ctx.Err() != nil {
				return c.ctx.Err()
			}
			child, _, err := c.stat(filepath.Join(from, d.Name()), false)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			copyChild(child)
		}
	}
	if len(failures) == 0 {
		fmt.Fprintf(os.Stdout, "copied directory %s -> %s\n", from, to)
	}
	return errors.Join(failures...)
}

type copyProgress struct {
	name         string
	total, bytes int64
	start, last  time.Time
}

func (p *copyProgress) show(done bool) {
	now := time.Now()
	if !done && now.Sub(p.last) < 250*time.Millisecond {
		return
	}
	p.last = now
	state := "copying"
	if done {
		state = "completed"
	}
	seconds := now.Sub(p.start).Seconds()
	fmt.Fprintf(os.Stderr, "%s: %d/%d bytes %.0f B/s %s\n", p.name, p.bytes, p.total, float64(p.bytes)/seconds, state)
}
func (c *copyClient) upload(from, to string, overwrite bool) (err error) {
	f, err := os.Open(from)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	var upload files.Upload
	if err := c.json("POST", "/api/nodes/"+c.node.ID+"/uploads", files.UploadRequest{Path: to, Size: info.Size(), Overwrite: overwrite}, &upload); err != nil {
		return err
	}
	endpoint := "/api/uploads/" + upload.ID
	defer func() {
		if err != nil && c.ctx.Err() == nil {
			cleanupErr := c.json("DELETE", endpoint, nil, nil)
			err = errors.Join(err, cleanupErr)
		}
	}()
	progress := copyProgress{name: from, total: info.Size(), start: time.Now()}
	progress.show(false)
	for offset := int64(0); offset < info.Size(); {
		length := min(int64(files.UploadChunk), info.Size()-offset)
		req, e := http.NewRequestWithContext(c.ctx, "PATCH", c.base+endpoint, io.NewSectionReader(f, offset, length))
		if e != nil {
			return e
		}
		req.ContentLength = length
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("X-Probex-File-Session", c.owner)
		req.Header.Set("Upload-Offset", fmt.Sprint(offset))
		response, e := c.response(req)
		if e != nil {
			return e
		}
		e = json.NewDecoder(response.Body).Decode(&upload)
		response.Body.Close()
		if e != nil {
			return e
		}
		if upload.Offset != offset+length {
			return fmt.Errorf("device acknowledged offset %d; expected %d", upload.Offset, offset+length)
		}
		offset = upload.Offset
		progress.bytes = offset
		progress.show(false)
	}
	if err := c.json("POST", endpoint+"/commit", nil, &upload); err != nil {
		return err
	}
	if upload.State != "completed" {
		return fmt.Errorf("device did not confirm upload completion")
	}
	progress.show(true)
	fmt.Fprintf(os.Stdout, "copied %s -> %s:%s\n", from, c.node.Name, to)
	return nil
}

type progressWriter struct {
	io.Writer
	progress *copyProgress
}

func (w progressWriter) Write(b []byte) (int, error) {
	n, err := w.Writer.Write(b)
	w.progress.bytes += int64(n)
	w.progress.show(false)
	return n, err
}
func (c *copyClient) download(from, to string, overwrite bool) (err error) {
	response, err := c.do("GET", c.remote("file", from), nil, 0)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	f, err := os.CreateTemp(filepath.Dir(to), ".probex-cp-*")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer func() {
		if f != nil {
			err = errors.Join(err, f.Close())
		}
		if temporary != "" {
			err = errors.Join(err, os.Remove(temporary))
		}
	}()
	progress := copyProgress{name: from, total: response.ContentLength, start: time.Now()}
	progress.show(false)
	if _, err := io.CopyBuffer(progressWriter{f, &progress}, response.Body, make([]byte, 64<<10)); err != nil {
		return err
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	mode := os.FileMode(0600)
	if info, e := os.Lstat(to); e == nil {
		if !overwrite {
			return fmt.Errorf("destination appeared during copy: %s", to)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("destination is not a regular file: %s", to)
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	closeErr := f.Close()
	f = nil
	if closeErr != nil {
		return closeErr
	}
	if overwrite {
		if err := os.Rename(temporary, to); err != nil {
			return err
		}
		temporary = ""
	} else if err := os.Link(temporary, to); err != nil {
		return fmt.Errorf("publish download: %w", err)
	}
	progress.show(true)
	fmt.Fprintf(os.Stdout, "copied %s:%s -> %s\n", c.node.Name, from, to)
	return nil
}
