package main

import (
	"errors"
	"fmt"
	"os"
	"probex/internal/protocol"
	"probex/internal/server"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("probexd", protocol.Version)
		return
	}
	if err := server.Main(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var exit *protocol.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.Code)
		}
		os.Exit(1)
	}
}
