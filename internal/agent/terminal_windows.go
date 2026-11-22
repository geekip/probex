package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

type windowsConsole struct {
	*conpty.ConPty
	once     sync.Once
	closeErr error
}

var setConsoleCtrlHandler = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")

func startConsole(cols, rows int) (console, error) {
	// SSH/process launchers may pass down an inheritable ignore-Ctrl+C flag.
	// Clear it before creating the shell; registered Go signal handlers remain.
	if ok, _, err := setConsoleCtrlHandler.Call(0, 0); ok == 0 {
		return nil, fmt.Errorf("enable console Ctrl+C: %w", err)
	}
	shell, err := exec.LookPath("powershell.exe")
	args := []string{"-NoLogo", "-NoProfile"}
	if errors.Is(err, exec.ErrNotFound) {
		shell, err = exec.LookPath("cmd.exe")
		args = nil
	}
	if err != nil {
		return nil, fmt.Errorf("find terminal shell: %w", err)
	}
	c, err := conpty.Start(windows.ComposeCommandLine(append([]string{shell}, args...)), conpty.ConPtyDimensions(cols, rows))
	if err != nil {
		return nil, err
	}
	return &windowsConsole{ConPty: c}, nil
}
func (c *windowsConsole) Wait() (int, error) {
	code, err := c.ConPty.Wait(context.Background())
	return int(code), err
}
func (c *windowsConsole) Close() error {
	c.once.Do(func() { c.closeErr = c.ConPty.Close() })
	return c.closeErr
}
func consoleEOF(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_INVALID_HANDLE)
}
