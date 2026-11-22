package main

import (
	"fmt"
	"os"
	"probex/internal/agent"
	"probex/internal/protocol"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("probex", protocol.Version)
		return
	}
	if err := agent.Main(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
