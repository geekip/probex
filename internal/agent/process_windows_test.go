package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWindowsCommandArgumentHelper(t *testing.T) {
	if os.Getenv("PROBEX_COMMAND_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			if err := json.NewEncoder(os.Stdout).Encode(os.Args[i+1:]); err != nil {
				os.Exit(3)
			}
			os.Exit(0)
		}
	}
	os.Exit(2)
}
func TestWindowsCommandKeepsShellQuotes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(t.TempDir(), "quoted tool.exe")
	if err := os.WriteFile(tool, data, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := shellCommand(ctx, `"`+tool+`" -test.run=^TestWindowsCommandArgumentHelper$ -- "value with spaces" "a&b" "中文 路径" ""`)
	command.Env = append(os.Environ(), "PROBEX_COMMAND_HELPER=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	var args []string
	if err := json.Unmarshal(output, &args); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	want := []string{"value with spaces", "a&b", "中文 路径", ""}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("got %q want %q", args, want)
	}
}
func TestWindowsCommandRedirectionAndExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := shellCommand(ctx, `echo "a&b" & echo error-text 1>&2 & exit /b 7`)
	var stderr strings.Builder
	command.Stderr = &stderr
	output, err := command.Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit: %v", err)
	}
	if !strings.Contains(string(output), `"a&b"`) || !strings.Contains(stderr.String(), "error-text") {
		t.Fatalf("stdout %q stderr %q", output, stderr.String())
	}
}
