package cmd

import (
	"flag"
	"fmt"
	"io"

	"github.com/flashdict/kindle2flashdict/internal/server"
	txtconfig "github.com/flashdict/kindle2flashdict/internal/txt2epub/config"
)

func Run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)

	webAddr := fs.String("web-addr", ":8787", "address for the desktop Web UI")
	kindleAddr := fs.String("kindle-addr", ":8788", "address for the Kindle download page")
	libraryDir := fs.String("library", "kindle-go-library", "library directory for uploads and converted files")
	configPath := fs.String("config", "", "txt2epub config file path (.toml, .yaml, .yml)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: serve [options]")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Options:")
		fs.PrintDefaults()
	}

	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fs.Usage()
		return nil
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: serve [options]")
	}

	baseCfg, _, err := txtconfig.Load(*configPath)
	if err != nil {
		return err
	}
	txtconfig.Normalize(&baseCfg)

	library, err := server.NewLibrary(*libraryDir)
	if err != nil {
		return err
	}
	handler := server.Handler{
		Library:    library,
		BaseConfig: baseCfg,
	}
	srv := server.Server{
		Config: server.Config{
			WebAddr:    *webAddr,
			KindleAddr: *kindleAddr,
			LibraryDir: *libraryDir,
		},
		Handler: handler,
		Stdout:  stdout,
	}
	return srv.Run()
}
