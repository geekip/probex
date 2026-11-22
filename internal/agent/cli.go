package agent

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func Main(args []string) error {
	if len(args) == 0 || args[0] != "connect" {
		return fmt.Errorf("usage: probex connect --server ws[s]://HOST [--key KEY | --name NAME] [--state FILE]")
	}
	f := flag.NewFlagSet("connect", flag.ContinueOnError)
	var c Config
	f.StringVar(&c.Server, "server", "", "server WebSocket URL")
	f.StringVar(&c.Name, "name", "", "stable node name")
	f.StringVar(&c.State, "state", "probex-state.json", "persistent identity file")
	f.StringVar(&c.CA, "ca", "", "custom CA certificate file")
	f.StringVar(&c.JoinKey, "key", "", "single-node join key")
	keyFile := f.String("key-file", "", "read join key from a file instead of command arguments")
	f.BoolVar(&c.Rejoin, "rejoin", false, "create a new identity and submit a new registration request")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if *keyFile != "" {
		if c.JoinKey != "" {
			return fmt.Errorf("use only one of --key and --key-file")
		}
		data, err := os.ReadFile(*keyFile)
		if err != nil {
			return err
		}
		c.JoinKey = strings.TrimSpace(string(data))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return Run(ctx, c)
}
