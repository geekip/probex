package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type consoleEvent struct{ output string }

func TestNativeTerminalDefaultUserDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	p := probeConsole(t, "")
	p.directory(t, home)
}

func TestNativeTerminalConfiguredUserDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "用户 home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	variable := "HOME"
	if runtime.GOOS == "windows" {
		variable = "USERPROFILE"
	}
	t.Setenv(variable, home)
	p := probeConsole(t, "")
	p.directory(t, home)
	explicit := t.TempDir()
	q := probeConsole(t, explicit)
	q.directory(t, explicit)
}

type consoleProbe struct {
	console
	events chan consoleEvent
	done   chan struct{}
	cancel context.CancelFunc
	waited bool
}

func probeConsole(t *testing.T, dir string) *consoleProbe {
	t.Helper()
	c, err := startConsole(240, 24, dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &consoleProbe{console: c, events: make(chan consoleEvent, 64), done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(p.done)
		defer close(p.events)
		emit := func(e consoleEvent) {
			select {
			case p.events <- e:
			case <-ctx.Done():
			}
		}
		b := make([]byte, 8192)
		for {
			n, err := c.Read(b)
			if n > 0 {
				emit(consoleEvent{output: string(b[:n])})
			}
			if err != nil {
				break
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		c.Stop()
		if !p.waited {
			c.Wait()
		}
		c.Close()
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			t.Error("PTY output reader retained")
		}
	})
	return p
}
func (p *consoleProbe) write(t *testing.T, command string) {
	t.Helper()
	if _, err := p.Write([]byte(command)); err != nil {
		t.Fatal(err)
	}
}
func (p *consoleProbe) until(t *testing.T, check func(consoleEvent) bool) string {
	t.Helper()
	timer := time.NewTimer(12 * time.Second)
	defer timer.Stop()
	var output strings.Builder
	for {
		select {
		case e, ok := <-p.events:
			if !ok {
				t.Fatalf("PTY ended early: length=%d suffix=%q", output.Len(), output.String()[max(0, output.Len()-2000):])
				return ""
			}
			output.WriteString(e.output)
			if check(consoleEvent{output: output.String()}) {
				return output.String()
			}
		case <-timer.C:
			t.Fatalf("PTY timed out: length=%d suffix=%q", output.Len(), output.String()[max(0, output.Len()-2000):])
			return ""
		}
	}
}

// Query only when the test asks; the terminal itself does not monitor directories.
func (p *consoleProbe) directory(t *testing.T, want string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatal(err)
	}
	want = resolved
	command := "printf '\\137DIR\\137%s\\n' \"$PWD\"\n"
	if runtime.GOOS == "windows" {
		// A fixed-length digest avoids ConPTY wrapping long temporary paths.
		command = "$h=[Security.Cryptography.SHA256]::Create().ComputeHash([Text.Encoding]::UTF8.GetBytes((Get-Location).ProviderPath.ToLowerInvariant())); Write-Output ('_D'+'IR_'+[BitConverter]::ToString($h).Replace('-','').ToLowerInvariant())\r"
		want = fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower(want))))
	}
	p.write(t, command)
	p.until(t, func(e consoleEvent) bool {
		return strings.Contains(strings.ToLower(e.output), strings.ToLower("_DIR_"+want))
	})
}
func TestNativeTerminalConsole(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(root, "中文 space")
	b := filepath.Join(root, "independent")
	for _, dir := range []string{a, b} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	first, second := probeConsole(t, root), probeConsole(t, b)
	first.directory(t, root)
	second.directory(t, b)
	if err := first.Resize(117, 37); err != nil {
		t.Fatal(err)
	}
	change, sizeMarker := nativeResizeCommand(t, first.console, a)
	t.Logf("expected cwd=%q size marker=%q", a, sizeMarker)
	wait := "/bin/sh -c 'printf \"\\137WAIT\\137\\n\"; exec sleep 30'\n"
	after := "printf '\\137AFTER\\137\\n'\n"
	if runtime.GOOS == "windows" {
		wait = "Write-Output ('_WAIT'+'_'); Start-Sleep -Seconds 30\r"
		after = "Write-Output ('_AFTER'+'_')\r"
	}
	first.write(t, change)
	if sizeMarker != "" {
		first.until(t, func(e consoleEvent) bool { return strings.Contains(e.output, sizeMarker) })
	}
	first.directory(t, a)
	first.write(t, wait)
	first.until(t, func(e consoleEvent) bool { return strings.Contains(e.output, "_WAIT_") })
	first.write(t, "\x03")
	first.until(t, func(e consoleEvent) bool {
		return strings.Contains(e.output, "^C") || strings.Contains(e.output, "PS ")
	})
	first.directory(t, a)
	first.write(t, after)
	first.until(t, func(e consoleEvent) bool { return strings.Contains(e.output, "_AFTER_") })
	// Closing one PTY cannot terminate or change its sibling's working directory.
	first.cancel()
	if err := first.Stop(); err != nil {
		t.Fatal(err)
	}
	first.Wait()
	first.waited = true
	second.write(t, after)
	second.until(t, func(e consoleEvent) bool { return strings.Contains(e.output, "_AFTER_") })
	second.directory(t, b)
	t.Logf("Explicit directory query, UTF-8, resize, Ctrl+C, two-session isolation: %s", runtime.GOOS)
}
func TestNativeTerminalOutputTail(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Unix tail verified through full tunnel integration")
	}
	directory := t.TempDir()
	p := probeConsole(t, directory)
	p.directory(t, directory)
	script := base64.StdEncoding.EncodeToString([]byte("[Console]::Write(('X'*2097152)+'_TAIL_'); exit 0"))
	p.write(t, "Invoke-Expression ([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('"+script+"')))\r")
	exited := make(chan error, 1)
	p.waited = true
	go func() {
		code, err := p.Wait()
		if err == nil && code != 0 {
			err = fmt.Errorf("exit %d", code)
		}
		p.Stop()
		exited <- err
	}()
	count := 0
	suffix := ""
	sawTail := false
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	reading := true
	for reading {
		select {
		case e, ok := <-p.events:
			if !ok {
				reading = false
				break
			}
			count += strings.Count(e.output, "X")
			suffix += e.output
			if strings.Contains(suffix, "_TAIL_") {
				sawTail = true
			}
			suffix = suffix[max(0, len(suffix)-128):]
		case <-deadline.C:
			t.Fatalf("tail timeout: count=%d tail=%v suffix=%q", count, sawTail, suffix)
		}
	}
	if count < 2097152 || !sawTail {
		t.Fatalf("truncated output: count=%d tail=%v suffix=%q", count, sawTail, suffix)
	}
	t.Logf("Windows console output X count=%d and tail marker received", count)

	if err := <-exited; err != nil {
		t.Fatal(err)
	}
	p.waited = true
}
