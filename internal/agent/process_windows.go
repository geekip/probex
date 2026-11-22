package agent

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

func shellCommand(ctx context.Context, text string) *exec.Cmd {
	c := exec.CommandContext(ctx, "cmd.exe", "/C", text)
	c.Cancel = func() error {
		return exec.Command("taskkill.exe", "/PID", strconv.Itoa(c.Process.Pid), "/T", "/F").Run()
	}
	c.WaitDelay = time.Second
	return c
}
