package cmd

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/vocab"
)

func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printUsage(stdout)
		return nil
	}
	if args[0] != "export" {
		return fmt.Errorf("unknown vocab command %q\n\nRun vocab help for usage.", args[0])
	}
	return runExport(args[1:], stdout, stderr)
}

func RunLegacyExport(args []string, stdout, stderr io.Writer) error {
	return runExport(args, stdout, stderr)
}

func runExport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("vocab export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "kindle2flashdict.toml", "path to config file")
	fs.Usage = func() {
		printExportUsage(stderr)
	}

	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		printExportUsage(stdout)
		return nil
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	return vocab.Export(*configPath, stdout)
}

func printUsage(out io.Writer) {
	fmt.Fprint(out, `Usage:
  vocab export [options]

Commands:
  export  Export Kindle Vocabulary Builder records to FlashDict card JSON.
`)
}

func printExportUsage(out io.Writer) {
	fmt.Fprint(out, `Usage:
  vocab export [options]

Options:
  -config string
        path to config file (default "kindle2flashdict.toml")
`)
}
