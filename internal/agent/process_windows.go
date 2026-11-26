package agent

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func shellCommand(ctx context.Context, text string) *exec.Cmd {
	c := exec.CommandContext(ctx, "cmd.exe", "/C", text)
	// cmd.exe has its own quoting rules; pass the shell text without argv escaping.
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: syscall.EscapeArg(c.Path) + ` /S /C "` + text + `"`}
	c.Cancel = func() error {
		return exec.Command("taskkill.exe", "/PID", strconv.Itoa(c.Process.Pid), "/T", "/F").Run()
	}
	c.WaitDelay = time.Second
	return c
}
