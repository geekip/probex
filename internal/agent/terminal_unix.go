//go:build !windows

package agent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/shirou/gopsutil/v4/process"
	"golang.org/x/sys/unix"
)

type unixConsole struct {
	*os.File
	cmd      *exec.Cmd
	once     sync.Once
	stopOnce sync.Once
	stopErr  error
	closeErr error
}

func startPlatformConsole(cols, rows int, cwd string) (console, error) {
	candidates := []string{"bash"}
	if runtime.GOOS == "darwin" {
		candidates = []string{"zsh", "bash"}
	}
	shell := "/bin/sh"
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err == nil {
			shell = path
			break
		}
		if !errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("find terminal shell: %w", err)
		}
	}
	c := exec.Command(shell, "-i")
	c.Dir = cwd
	c.Env = terminalEnvironment()
	f, err := pty.StartWithSize(c, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixConsole{File: f, cmd: c}, nil
}

func terminalEnvironment() []string {
	configured := os.Getenv("TERMINFO")
	dirs := []string{configured}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".terminfo"))
	}
	dirs = append(dirs, filepath.SplitList(os.Getenv("TERMINFO_DIRS"))...)
	dirs = append(dirs, "/etc/terminfo", "/lib/terminfo", "/usr/share/terminfo", "/usr/lib/terminfo")
	name, dir := terminalDefinition(dirs)
	// A prompt exported by the launching shell may use incompatible syntax.
	env := slices.DeleteFunc(os.Environ(), func(entry string) bool {
		return strings.HasPrefix(entry, "PS1=")
	})
	env = append(env, "TERM="+name)
	// Some embedded libncurses builds have a build-time path as their default.
	if configured == "" && dir != "" {
		env = append(env, "TERMINFO="+dir)
	}
	return env
}

func terminalDefinition(dirs []string) (name, dir string) {
	for _, name := range []string{"xterm-256color", "xterm", "vt100", "linux"} {
		prefix := name[:1]
		hexPrefix := strconv.FormatUint(uint64(prefix[0]), 16)
		for _, dir := range dirs {
			if dir == "" {
				continue
			}
			for _, subdir := range []string{prefix, hexPrefix} {
				info, err := os.Stat(filepath.Join(dir, subdir, name))
				if err == nil && info.Mode().IsRegular() && info.Size() > 0 {
					return name, dir
				}
			}
		}
	}
	return "xterm-256color", ""
}

func (c *unixConsole) Resize(cols, rows int) error {
	return pty.Setsize(c.File, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}
func (c *unixConsole) Wait() (int, error) {
	err := c.cmd.Wait()
	return c.cmd.ProcessState.ExitCode(), err
}
func (c *unixConsole) Close() error {
	c.once.Do(func() {
		c.closeErr = errors.Join(c.Stop(), c.File.Close())
	})
	return c.closeErr
}
func (c *unixConsole) Stop() error {
	c.stopOnce.Do(func() {
		// Interactive job control gives foreground/background jobs their own
		// process groups. Reclaim the PTY session, not just the shell's group.
		pids, err := process.Pids()
		c.stopErr = err
		for _, pid := range pids {
			if int(pid) == c.cmd.Process.Pid {
				continue
			}
			sid, err := unix.Getsid(int(pid))
			if err == nil && sid == c.cmd.Process.Pid {
				if err := syscall.Kill(int(pid), syscall.SIGKILL); err != nil && err != syscall.ESRCH {
					c.stopErr = errors.Join(c.stopErr, err)
				}
			}
		}
		if err := syscall.Kill(c.cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			c.stopErr = errors.Join(c.stopErr, err)
		}
	})
	return c.stopErr
}
func consoleEOF(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed)
}
