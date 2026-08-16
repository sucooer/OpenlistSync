package config

import (
	"flag"
	"fmt"
	"os"
)

// WebConfig holds options for the `web` subcommand.
type WebConfig struct {
	Listen   string
	Store    string
	APIToken string
}

// ParseWeb parses `openlist-sync web` flags, falling back to env vars.
func ParseWeb(args []string) (*WebConfig, error) {
	fs := flag.NewFlagSet("openlist-sync web", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: openlist-sync web [flags]\n\nWeb 管理界面 + 调度器 (OpenListSync server)\n\nFlags:\n")
		fs.PrintDefaults()
	}
	listen := fs.String("listen", envStr("WEB_LISTEN", ":18222"), "HTTP listen address")
	store := fs.String("store", envStr("WEB_STORE", "openlist-sync.json"), "config storage file (created if missing)")
	token := fs.String("api-token", envStr("WEB_API_TOKEN", ""), "API token for the web UI (empty = no auth)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	return &WebConfig{Listen: *listen, Store: *store, APIToken: *token}, nil
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}