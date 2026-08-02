package main

import (
	"fmt"
	"os"
	"strings"

	servercmd "github.com/flashdict/kindle2flashdict/internal/server/cmd"
	transfercmd "github.com/flashdict/kindle2flashdict/internal/transfer/cmd"
	txt2epubcmd "github.com/flashdict/kindle2flashdict/internal/txt2epub/cmd"
	vocabcmd "github.com/flashdict/kindle2flashdict/internal/vocab/cmd"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printUsage(os.Stdout)
		return nil
	}
	if strings.HasPrefix(args[0], "-") {
		return vocabcmd.RunLegacyExport(args, os.Stdout, os.Stderr)
	}

	switch args[0] {
	case "vocab":
		return vocabcmd.Run(args[1:], os.Stdout, os.Stderr)
	case "txt2epub":
		return txt2epubcmd.Run(args[1:], os.Stdout, os.Stderr)
	case "serve":
		return servercmd.Run(args[1:], os.Stdout, os.Stderr)
	case "transfer":
		return transfercmd.Run(args[1:], os.Stdout, os.Stderr)
	default:
		return fmt.Errorf("unknown command %q\n\nRun %s help for usage.", args[0], os.Args[0])
	}
}

func printUsage(out *os.File) {
	fmt.Fprintf(out, `Kindle toolbox CLI.

Usage:
  %s vocab export [options]
  %s txt2epub [options] input.txt|input.epub
  %s serve [options]
  %s transfer <control|storage|r2-probe> [options]

Commands:
  vocab export  Export Kindle Vocabulary Builder records to FlashDict card JSON.
  txt2epub      Convert TXT to EPUB/AZW3 or EPUB to native AZW3.
  serve         Run a LAN Web UI and Kindle download page.
  transfer      Transfer files by six-digit code via WebRTC, R2, or Server A.

Run "%s <command> help" for command-specific usage.
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0])
}
