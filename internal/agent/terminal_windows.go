package agent

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"github.com/UserExistsError/conpty"
	"golang.org/x/sys/windows"
)

type windowsConsole struct {
	*conpty.ConPty
	once     sync.Once
	closeErr error
	job      windows.Handle
	process  windows.Handle
}

var setConsoleCtrlHandler = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler")

func windowsTerminalCommand() ([]string, error) {
	for _, candidate := range []string{"pwsh.exe", "powershell.exe"} {
		shell, err := exec.LookPath(candidate)
		if err == nil {
			return []string{shell, "-NoLogo", "-NoProfile", "-NoExit"}, nil
		}
		if !errors.Is(err, exec.ErrNotFound) {
			return nil, err
		}
	}
	shell, err := exec.LookPath("cmd.exe")
	return []string{shell, "/Q"}, err
}

func startPlatformConsole(cols, rows int, cwd string) (console, error) {
	// SSH/process launchers may pass down an inheritable ignore-Ctrl+C flag.
	// Clear it before creating the shell; registered Go signal handlers remain.
	if ok, _, err := setConsoleCtrlHandler.Call(0, 0); ok == 0 {
		return nil, fmt.Errorf("enable console Ctrl+C: %w", err)
	}
	argv, err := windowsTerminalCommand()
	if err != nil {
		return nil, fmt.Errorf("find terminal shell: %w", err)
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	c, err := conpty.Start(windows.ComposeCommandLine(argv), conpty.ConPtyDimensions(cols, rows), conpty.ConPtyWorkDir(cwd), conpty.ConPtyEnv(os.Environ()))
	if err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(c.Pid()))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, process)
	}
	if err != nil {
		go io.Copy(io.Discard, c)
		c.Close()
		windows.CloseHandle(job)
		if process != 0 {
			windows.CloseHandle(process)
		}
		return nil, err
	}
	return &windowsConsole{ConPty: c, job: job, process: process}, nil
}
func (c *windowsConsole) Wait() (int, error) {
	defer windows.CloseHandle(c.process)
	_, err := windows.WaitForSingleObject(c.process, windows.INFINITE)
	var code uint32
	if err == nil {
		err = windows.GetExitCodeProcess(c.process, &code)
	}
	return int(code), err
}
func (c *windowsConsole) Close() error {
	c.once.Do(func() { c.closeErr = errors.Join(windows.CloseHandle(c.job), c.ConPty.Close()) })
	return c.closeErr
}
func (c *windowsConsole) Stop() error { return c.Close() }
func consoleEOF(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_INVALID_HANDLE)
}
