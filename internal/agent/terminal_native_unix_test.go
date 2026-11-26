//go:build !windows

package agent

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/shirou/gopsutil/v4/process"
)

func nativeResizeCommand(t *testing.T, c console, cwd string) (string, string) {
	t.Helper()
	rows, cols, err := pty.Getsize(c.(*unixConsole).File)
	if err != nil || cols != 117 || rows != 37 {
		t.Fatalf("PTY dimensions %dx%d: %v", cols, rows, err)
	}
	return "cd '" + cwd + "'\n", ""
}

func TestTerminalDefinitionPrefersInstalledCompatibleType(t *testing.T) {
	primary, secondary := t.TempDir(), t.TempDir()
	add := func(root, subdir, name string) {
		t.Helper()
		dir := filepath.Join(root, subdir)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("definition"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(name, dir string) {
		t.Helper()
		if selected, path := terminalDefinition([]string{primary, secondary}); selected != name || path != dir {
			t.Fatalf("selected %q in %q, wanted %q in %q", selected, path, name, dir)
		}
	}
	check("xterm-256color", "")
	add(primary, "76", "vt100")
	check("vt100", primary)
	add(secondary, "x", "xterm")
	check("xterm", secondary)
	add(primary, "78", "xterm-256color")
	check("xterm-256color", primary)
}

func TestNativeTerminalIgnoresInheritedPrompt(t *testing.T) {
	t.Setenv("PS1", `\[\033]9003;/root;;;\007\]\u@\h:\w$ \[\033]9010;\007\]`)
	t.Setenv("PATH", t.TempDir())
	p := probeConsole(t, t.TempDir())
	p.write(t, "printf '%s%s\\n' 'TERMINAL-' 'READY'\n")
	output := p.until(t, func(e consoleEvent) bool {
		return strings.Contains(e.output, "TERMINAL-READY")
	})
	if strings.Contains(output, "9003;") || strings.Contains(output, "9010;") {
		t.Fatalf("terminal printed the parent shell's PS1: %q", output)
	}
}

func TestNativeTerminalShellPriority(t *testing.T) {
	first := "bash"
	if runtime.GOOS == "darwin" {
		first = "zsh"
	}
	for _, tc := range []struct {
		name       string
		candidates []string
		want       string
	}{
		{"all installed", []string{"zsh", "bash"}, first},
		{"bash only", []string{"bash"}, "bash"},
		{"no preferred shell", nil, "/bin/sh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range tc.candidates {
				if err := os.Symlink("/bin/sh", filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", dir)
			want := tc.want
			if !filepath.IsAbs(want) {
				want = filepath.Join(dir, want)
			}
			p := probeConsole(t, dir)
			if got := p.console.(*unixConsole).cmd.Path; got != want {
				t.Fatalf("shell = %q, want %q", got, want)
			}
			p.directory(t, dir)
		})
	}
}

func TestNativeTerminalChildJobs(t *testing.T) {
	dir := t.TempDir()
	p := probeConsole(t, dir)
	bg, fg := filepath.Join(dir, "bg.pid"), filepath.Join(dir, "fg.pid")
	p.write(t, "sleep 60 & echo $! > '"+bg+"'; /bin/sh -c 'echo $$ > \""+fg+"\"; exec sleep 60'\n")
	var pids []int
	for _, path := range []string{bg, fg} {
		deadline := time.Now().Add(5 * time.Second)
		for {
			data, err := os.ReadFile(path)
			if err == nil {
				pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
				if err != nil {
					t.Fatal(err)
				}
				pids = append(pids, pid)
				break
			}
			if !os.IsNotExist(err) || time.Now().After(deadline) {
				t.Fatal("child job not started", err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	p.cancel()
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	p.Wait()
	p.waited = true
	for _, pid := range pids {
		deadline := time.Now().Add(3 * time.Second)
		for {
			err := syscall.Kill(pid, 0)
			if err == syscall.ESRCH {
				break
			}
			child, err := process.NewProcess(int32(pid))
			if errors.Is(err, process.ErrorProcessNotRunning) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			statuses, err := child.Status()
			if err == nil && slices.Contains(statuses, process.Zombie) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("PTY retained job %d, status=%v error=%v", pid, statuses, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Log("foreground and background PTY jobs reclaimed")
}
