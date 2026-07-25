package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/config"
	"github.com/flashdict/kindle2flashdict/internal/generation"
	"github.com/flashdict/kindle2flashdict/internal/library"
	"github.com/flashdict/kindle2flashdict/internal/proofread"
	"github.com/flashdict/kindle2flashdict/internal/server"
	appsettings "github.com/flashdict/kindle2flashdict/internal/settings"
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

	baseCfg, resolvedConfig, err := txtconfig.Load(*configPath)
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
	runtime := appsettings.Runtime{
		LibraryDir: *libraryDir, LibrarySource: settingSource(visited["library"], resolvedConfig),
		WebAddr: *webAddr, WebAddrSource: settingSource(visited["web-addr"], resolvedConfig),
		KindleAddr: *kindleAddr, KindleSource: settingSource(visited["kindle-addr"], resolvedConfig),
		ConfigPath: resolvedConfig,
	}
	settingsService := appsettings.New(storage, baseCfg, runtime)
	if err := settingsService.Initialize(context.Background()); err != nil {
		return err
	}
	taskService := task.NewService(storage)
	generationService := generation.NewService(libraryService, taskService, settingsService)
	generationExecutor := generation.NewExecutor(libraryService)
	proofreadService := proofread.NewService(storage, libraryService, taskService, settingsService)
	proofreadExecutor := proofread.NewExecutor(storage, libraryService, nil, nil)
	revisionExecutor := proofread.NewRevisionExecutor(storage, libraryService, nil)
	runner := task.NewRunner(taskService, map[task.Type]task.Executor{
		task.GenerateEPUB:      generationExecutor,
		task.GenerateAZW3:      generationExecutor,
		task.Proofread:         proofreadExecutor,
		task.BuildRevisionTXT:  revisionExecutor,
		task.BuildRevisionEPUB: revisionExecutor,
	})
	runner.SetLogWriter(stdout)
	handler := server.NewHandler(libraryService, generationService, taskService, settingsService, proofreadService)
	if stdout != nil {
		handler.Logger = log.New(stdout, "", log.LstdFlags)
	}
	srv := server.NewServer(server.Config{WebAddr: *webAddr, KindleAddr: *kindleAddr}, handler, runner, stdout)
	return srv.Run()
}

func settingSource(cli bool, configPath string) string {
	if cli {
		return "CLI"
	}
	if configPath != "" {
		return "config file"
	}
	return "code default"
}
