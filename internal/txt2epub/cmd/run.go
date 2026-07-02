package cmd

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/txt2epub/app"
	"github.com/flashdict/kindle2flashdict/internal/txt2epub/config"
)

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func Run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("txt2epub", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var flags cliFlags
	var dropRegex stringList
	var replaceRegex stringList
	var calibreArgs stringList

	fs.StringVar(&flags.configPath, "config", "", "config file path (.toml, .yaml, .yml)")
	fs.StringVar(&flags.output, "o", "", "output file path")
	fs.StringVar(&flags.output, "output", "", "output file path")
	fs.StringVar(&flags.title, "title", "", "book title")
	fs.StringVar(&flags.author, "author", "", "book author")
	fs.StringVar(&flags.language, "language", "", "book language")
	fs.StringVar(&flags.h1Regex, "h1-regex", "", "level 1 heading regex")
	fs.StringVar(&flags.h2Regex, "h2-regex", "", "level 2 heading regex")
	fs.StringVar(&flags.format, "format", "", "output format: epub or azw3")
	fs.IntVar(&flags.splitLevel, "split-level", 0, "chapter split level: 1 or 2")
	fs.BoolVar(&flags.preview, "preview", false, "preview matched table of contents without writing EPUB")
	fs.BoolVar(&flags.verbose, "verbose", false, "print detailed parse summary")
	fs.BoolVar(&flags.noCover, "no-cover", false, "disable generated text cover")
	fs.BoolVar(&flags.noMergeLines, "no-merge-lines", false, "disable hard-wrapped paragraph merging")
	fs.BoolVar(&flags.noTrimBlankLines, "no-trim-blank-lines", false, "disable repeated blank line trimming")
	fs.Var(&dropRegex, "drop-regex", "drop lines matching regex; repeatable")
	fs.Var(&replaceRegex, "replace-regex", "replace regex in pattern=replacement form; repeatable")
	fs.StringVar(&flags.calibrePath, "calibre-path", "", "path to ebook-convert")
	fs.Var(&calibreArgs, "calibre-arg", "extra argument passed to ebook-convert; repeatable")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: txt2epub [options] input.txt")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Options:")
		fs.PrintDefaults()
	}

	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fs.Usage()
		return nil
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: txt2epub [options] input.txt")
	}

	cfg, _, err := config.Load(flags.configPath)
	if err != nil {
		return err
	}
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		visited[f.Name] = true
	})
	applyCLI(&cfg, flags, dropRegex, replaceRegex, calibreArgs, visited)
	config.Normalize(&cfg)

	return app.Run(fs.Arg(0), cfg, app.Options{
		ConfigPath: flags.configPath,
		Preview:    flags.preview,
		Verbose:    flags.verbose,
	}, stdout)
}

type cliFlags struct {
	configPath       string
	output           string
	title            string
	author           string
	language         string
	h1Regex          string
	h2Regex          string
	format           string
	splitLevel       int
	preview          bool
	verbose          bool
	noCover          bool
	noMergeLines     bool
	noTrimBlankLines bool
	calibrePath      string
}

func applyCLI(cfg *config.Config, flags cliFlags, dropRegex, replaceRegex, calibreArgs []string, visited map[string]bool) {
	if visited["o"] || visited["output"] {
		cfg.Output = flags.output
	}
	if visited["title"] {
		cfg.Title = flags.title
	}
	if visited["author"] {
		cfg.Author = flags.author
	}
	if visited["language"] {
		cfg.Language = flags.language
	}
	if visited["h1-regex"] {
		cfg.H1Regex = flags.h1Regex
	}
	if visited["h2-regex"] {
		cfg.H2Regex = flags.h2Regex
	}
	if visited["format"] {
		cfg.Format = flags.format
	}
	if visited["split-level"] {
		cfg.SplitLevel = flags.splitLevel
	}
	if visited["no-cover"] {
		cfg.Cover = false
	}
	if visited["no-merge-lines"] {
		cfg.MergeLines = false
	}
	if visited["no-trim-blank-lines"] {
		cfg.TrimBlankLines = false
	}
	if len(dropRegex) > 0 {
		cfg.DropRegex = append(cfg.DropRegex, dropRegex...)
	}
	for _, raw := range replaceRegex {
		pattern, with, ok := strings.Cut(raw, "=")
		if !ok {
			pattern, with = raw, ""
		}
		cfg.Replace = append(cfg.Replace, config.ReplaceRule{Pattern: pattern, With: with})
	}
	if visited["calibre-path"] {
		cfg.Calibre.Path = flags.calibrePath
	}
	if len(calibreArgs) > 0 {
		cfg.Calibre.ExtraArgs = append(cfg.Calibre.ExtraArgs, calibreArgs...)
	}
}
