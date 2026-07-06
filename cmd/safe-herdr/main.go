package main

import (
	"os"

	"github.com/liu-qingyuan/safe-claude-sbx/internal/launcher"
)

func main() {
	os.Exit(launcher.RunSafeHerdr(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
