package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	txt2epubcmd "github.com/flashdict/kindle2flashdict/internal/txt2epub/cmd"
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
		return runVocabExport(args)
	}

	switch args[0] {
	case "vocab":
		return runVocabCommand(args[1:])
	case "txt2epub":
		return txt2epubcmd.Run(args[1:], os.Stdout, os.Stderr)
	default:
		return fmt.Errorf("unknown command %q\n\nRun %s help for usage.", args[0], os.Args[0])
	}
}

func runVocabCommand(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printVocabUsage(os.Stdout)
		return nil
	}
	if args[0] != "export" {
		return fmt.Errorf("unknown vocab command %q\n\nRun %s vocab help for usage.", args[0], os.Args[0])
	}
	return runVocabExport(args[1:])
}

func runVocabExport(args []string) error {
	fs := flag.NewFlagSet("vocab export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "kindle2flashdict.toml", "path to config file")
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		printVocabUsage(os.Stdout)
		return nil
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}

	records, skippedMissingUsage, err := ReadKindleRecords(cfg.Kindle.VocabDB)
	if err != nil {
		return err
	}
	deduped := DeduplicateRecords(records)
	summary := Summary{
		RawRecords:          len(records) + skippedMissingUsage,
		SkippedMissingUsage: skippedMissingUsage,
		DedupedRecords:      len(deduped),
	}

	lookups, reviews, lookupFailures, err := LookupFlashDictSenses(cfg, deduped)
	if err != nil {
		return err
	}
	summary.LookupFailures = lookupFailures

	cards, aiReviews, aiLowConfidence, aiInvalid, err := SelectAndBuildCards(cfg, deduped, lookups)
	if err != nil {
		return err
	}
	reviews = append(reviews, aiReviews...)
	summary.AILowConfidence = aiLowConfidence
	summary.AIInvalid = aiInvalid
	summary.Cards = len(cards)
	summary.ReviewRecords = len(reviews)

	if err := WriteFlashcardEnvelope(cfg.Output.Path, cards); err != nil {
		return err
	}
	if err := WriteJSONL(cfg.Output.ReviewPath, reviews); err != nil {
		return err
	}
	PrintSummary(summary, cfg)
	return nil
}

func printUsage(out *os.File) {
	fmt.Fprintf(out, `Kindle toolbox CLI.

Usage:
  %s vocab export [options]
  %s txt2epub [options] input.txt

Commands:
  vocab export  Export Kindle Vocabulary Builder records to FlashDict card JSON.
  txt2epub      Convert a TXT book to EPUB, or AZW3 through Calibre.

Run "%s <command> help" for command-specific usage.
`, os.Args[0], os.Args[0], os.Args[0])
}

func printVocabUsage(out *os.File) {
	fmt.Fprintf(out, `Usage:
  %s vocab export [options]

Options:
  -config string
        path to config file (default "kindle2flashdict.toml")
`, os.Args[0])
}
