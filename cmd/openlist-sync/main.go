package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"openlist-sync/internal/client"
	"openlist-sync/internal/config"
	"openlist-sync/internal/daemon"
	"openlist-sync/internal/syncer"
	"openlist-sync/internal/web"
)

var version = "dev"

func main() {
	sub := "sync"
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "sync", "daemon", "version", "web":
			sub = args[0]
			args = args[1:]
		}
	}

	if sub == "version" {
		fmt.Printf("openlist-sync %s\n", version)
		return
	}

	if sub == "web" {
		wc, err := config.ParseWeb(args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "openlist-sync web: %v\n", err)
			os.Exit(2)
		}
		logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmsgprefix)
		logf := func(format string, a ...any) { logger.Printf(format, a...) }

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := web.Run(ctx, wc.Listen, wc.Store, wc.APIToken, version, logf); err != nil {
			logf("web server exited: %v", err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Parse(sub, args)
	if err != nil {
		if err == config.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "openlist-sync: %v\n", err)
		os.Exit(2)
	}
	if cfg.Version {
		fmt.Printf("openlist-sync %s\n", version)
		return
	}

	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmsgprefix)
	if cfg.Verbose {
		logger.SetFlags(log.LstdFlags | log.Lmsgprefix | log.Lmicroseconds)
	}
	logf := func(format string, a ...any) { logger.Printf(format, a...) }

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if sub == "daemon" {
		if err := daemon.Run(ctx, cfg, logf); err != nil {
			logf("daemon exited: %v", err)
			os.Exit(1)
		}
		return
	}

	c := client.New(cfg.BaseURL, cfg.Token, cfg.Username, cfg.Password, cfg.DownloadMode == "proxy", logf)
	s := syncer.New(cfg, c, logf)
	if err := s.Run(ctx); err != nil {
		logf("sync failed: %v", err)
		os.Exit(1)
	}
}