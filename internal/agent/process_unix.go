//go:build !windows

package agent

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func shellCommand(ctx context.Context, text string) *exec.Cmd {
	c := exec.CommandContext(ctx, "/bin/sh", "-c", text)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	c.WaitDelay = time.Second
	return c
}
