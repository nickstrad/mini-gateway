// Command detlint is the determinism lint for sim-reachable packages.
//
//	detlint [-tests] [-allow file] [-allow-import pkg,...] -root <module root> <pkg-dir>...
//
// Exit status: 0 clean, 1 findings, 2 usage or internal error.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"detlint/internal/report"
)

func main() {
	os.Exit(mainArgs(os.Args[1:]))
}

func mainArgs(args []string) int {
	fl := flag.NewFlagSet("detlint", flag.ContinueOnError)
	var cfg config
	fl.StringVar(&cfg.root, "root", "", "module root directory (contains go.mod)")
	fl.BoolVar(&cfg.tests, "tests", false, "also check _test.go files")
	fl.StringVar(&cfg.allow, "allow", "", "allowlist file: path<TAB>Func<TAB>rule<TAB>justification")
	allowImport := fl.String("allow-import", "", "comma-separated import paths added to the import allowlist")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	cfg.pkgs = fl.Args()
	for _, p := range strings.Split(*allowImport, ",") {
		if p = strings.TrimSpace(p); p != "" {
			cfg.allowImports = append(cfg.allowImports, p)
		}
	}
	if cfg.root == "" || len(cfg.pkgs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: detlint [-tests] [-allow file] [-allow-import pkg,...] -root <module root> <pkg-dir>...")
		return 2
	}
	res, err := run(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "detlint:", err)
		return 2
	}
	report.Print(os.Stdout, res.findings)
	fmt.Fprintf(os.Stderr, "detlint: checked %d files in %d packages\n", res.files, res.pkgs)
	return report.ExitCode(res.findings, nil)
}
