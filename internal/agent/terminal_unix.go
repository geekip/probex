//go:build !windows

package agent

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

type unixConsole struct {
	*os.File
	cmd      *exec.Cmd
	once     sync.Once
	closeErr error
}

func startConsole(cols, rows int) (console, error) {
	c := exec.Command("/bin/sh")
	c.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(c, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	return &unixConsole{File: f, cmd: c}, nil
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
		c.closeErr = c.File.Close()
		err := syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
		if c.closeErr == nil && err != nil && err != syscall.ESRCH {
			c.closeErr = err
		}
	})
	return c.closeErr
}
func consoleEOF(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed)
}
