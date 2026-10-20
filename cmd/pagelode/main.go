package main

import (
	"fmt"
	"os"

	"github.com/simonbalfe/pagelode/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "pagelode:", err)
		os.Exit(1)
	}
}
