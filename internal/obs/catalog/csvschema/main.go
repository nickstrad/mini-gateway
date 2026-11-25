package main

import (
	"fmt"
	"os"

	"github.com/nickstrad/mini-gateway/internal/obs/catalog"
)

func main() {
	b, err := catalog.Markdown()
	if err != nil {
		fmt.Fprintln(os.Stderr, "csvschema:", err)
		os.Exit(1)
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: csvschema <output file>")
		os.Exit(2)
	}
	if err := os.WriteFile(os.Args[1], b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "csvschema:", err)
		os.Exit(1)
	}
}
