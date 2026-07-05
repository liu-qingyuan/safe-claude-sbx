package main

import (
	"fmt"
	"io"
	"os"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/config"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 3 || args[0] != "doctor" || args[1] != "--config" {
		fmt.Fprintln(stderr, "usage: safe-claude-sbx doctor --config <file>")
		return 2
	}

	if err := config.LoadAndValidate(args[2]); err != nil {
		fmt.Fprintf(stderr, "configuration invalid: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, "configuration ok")
	return 0
}
