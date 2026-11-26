package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestNativeTerminalShellPriority(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"cmd.exe", []string{"/Q"}},
		{"powershell.exe", []string{"-NoLogo", "-NoProfile", "-NoExit"}},
		{"pwsh.exe", []string{"-NoLogo", "-NoProfile", "-NoExit"}},
	} {
		path := filepath.Join(dir, tc.name)
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		argv, err := windowsTerminalCommand()
		if err != nil {
			t.Fatal(err)
		}
		want := append([]string{path}, tc.args...)
		if !slices.Equal(argv, want) {
			t.Fatalf("shell command = %q, want %q", argv, want)
		}
	}
}

func TestNativeTerminalCmdFallback(t *testing.T) {
	dir := t.TempDir()
	binary, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd.exe"), binary, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	p := probeConsole(t, "")
	p.write(t, "echo _D^IR_%CD%\r")
	p.until(t, func(e consoleEvent) bool {
		return strings.Contains(strings.ToLower(e.output), strings.ToLower("_DIR_"+home))
	})
	child := filepath.Join(dir, "中文 space")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	p.write(t, "cd /d \""+child+"\"\r")
	p.write(t, "echo _D^IR_%CD%\r")
	p.until(t, func(e consoleEvent) bool {
		return strings.Contains(strings.ToLower(e.output), strings.ToLower("_DIR_"+child))
	})
	p.write(t, "echo _CMD_^_READY\r")
	p.until(t, func(e consoleEvent) bool { return strings.Contains(e.output, "_CMD__READY") })
	t.Log("cmd-only PATH fallback accepts explicit directory queries")
}

func nativeResizeCommand(t *testing.T, c console, cwd string) (string, string) {
	return "Set-Location -LiteralPath '" + cwd + "'; Write-Output ('_SIZE_'+$Host.UI.RawUI.WindowSize.Width+' '+$Host.UI.RawUI.WindowSize.Height)\r", "_SIZE_117 37"
}

func TestNativeTerminalChildJobs(t *testing.T) {
	dir := t.TempDir()
	p := probeConsole(t, dir)
	p.directory(t, dir)
	pidFile := filepath.Join(dir, "child.pid")
	p.write(t, "$p=Start-Process powershell.exe -ArgumentList '-NoLogo','-NoProfile','-Command','Start-Sleep -Seconds 60' -PassThru; [IO.File]::WriteAllText('"+pidFile+"',[string]$p.Id)\r")
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		if !os.IsNotExist(err) || time.Now().After(deadline) {
			t.Fatal("child not started", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	child, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(child)
	if state, err := windows.WaitForSingleObject(child, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("child not running", state, err)
	}
	p.cancel()
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	p.Wait()
	p.waited = true
	if state, err := windows.WaitForSingleObject(child, 5000); err != nil || state != uint32(windows.WAIT_OBJECT_0) {
		t.Fatal("PTY retained child", state, err)
	}
	t.Log("detached Start-Process child reclaimed by per-terminal job")
}
