package cmd

import (
	"flag"
	"fmt"
	"io"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/generation"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/server"
	"github.com/flashdict/kindle2flashdict/internal/store"
	"github.com/flashdict/kindle2flashdict/internal/task"
)

func Run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)

	webAddr := fs.String("web-addr", "", "override address for the desktop Web UI")
	kindleAddr := fs.String("kindle-addr", "", "override address for the Kindle download page")
	libraryDir := fs.String("library", "", "override library directory for uploads and converted files")
	configPath := fs.String("config", "", "toolbox config file path (.toml, .yaml, .yml)")
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
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	if !visited["web-addr"] {
		*webAddr = baseCfg.Server.WebAddr
	}
	if !visited["kindle-addr"] {
		*kindleAddr = baseCfg.Server.KindleAddr
	}
	if !visited["library"] {
		*libraryDir = baseCfg.Server.LibraryDir
	}

	storage, err := store.OpenForWorker(*libraryDir)
	if err != nil {
		return err
	}
	libraryService := library.New(storage)
	defer libraryService.Close()
	taskService := task.NewService(storage)
	generationService := generation.NewService(libraryService, taskService, baseCfg)
	generationExecutor := generation.NewExecutor(libraryService)
	runner := task.NewRunner(taskService, map[task.Type]task.Executor{
		task.GenerateEPUB: generationExecutor,
		task.GenerateAZW3: generationExecutor,
	})
	handler := server.Handler{
		Library:    libraryService,
		Generation: generationService,
	}
	srv := server.Server{
		Config: server.Config{
			WebAddr:    *webAddr,
			KindleAddr: *kindleAddr,
			LibraryDir: *libraryDir,
		},
		Handler: handler,
		Runner:  runner,
		Stdout:  stdout,
	}
	return srv.Run()
}
