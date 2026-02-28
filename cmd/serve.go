package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"gocode-router/internal/config"
	"gocode-router/internal/provider"
	providerfactory "gocode-router/internal/provider/factory"
	"gocode-router/internal/router"
	"gocode-router/internal/server"
)

const serveUsage = `Usage:
  gocode-router serve --config <path> [--port <port>]

Flags:
  --config string   Path to YAML configuration file (required)
  --port   int      Override server port from configuration`

func serve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, serveUsage)
	}

	var cfgPath string
	var overridePort int
	fs.StringVar(&cfgPath, "config", "", "path to configuration file")
	fs.IntVar(&overridePort, "port", 0, "override server port")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("parse serve flags: %w", err)
	}

	if cfgPath == "" {
		return errors.New("serve command requires --config <path>")
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	if overridePort != 0 {
		if overridePort <= 0 || overridePort > 65535 {
			return fmt.Errorf("port override %d must be a valid TCP port", overridePort)
		}
		cfg.Server.Port = overridePort
	}

	rt, err := buildRouter(ctx, cfg)
	if err != nil {
		return err
	}

	srv, err := server.New(cfg, rt)
	if err != nil {
		return err
	}

	absCfgPath, err := filepath.Abs(cfgPath)
	if err != nil {
		return fmt.Errorf("resolve config path: %w", err)
	}

	info, err := os.Stat(absCfgPath)
	if err != nil {
		return fmt.Errorf("stat config file: %w", err)
	}

	go watchConfigFile(ctx, srv, absCfgPath, info.ModTime(), overridePort)

	return srv.Run(ctx)
}

func buildRouter(ctx context.Context, cfg config.Config) (*router.Router, error) {
	registry := provider.NewRegistry()
	if err := providerfactory.RegisterConfiguredProviders(ctx, cfg, registry); err != nil {
		return nil, err
	}
	return router.New(registry), nil
}

func watchConfigFile(ctx context.Context, srv *server.Server, cfgPath string, lastMod time.Time, overridePort int) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	slog.Info("hot reload enabled", "path", cfgPath)

	for {
		select {
		case <-ctx.Done():
			slog.Debug("config watcher shutting down", "path", cfgPath)
			return
		case <-ticker.C:
			info, err := os.Stat(cfgPath)
			if err != nil {
				slog.Warn("config watcher stat failed", "path", cfgPath, "error", err)
				continue
			}

			modTime := info.ModTime()
			if !modTime.After(lastMod) {
				continue
			}

			cfg, err := config.Load(cfgPath)
			if err != nil {
				slog.Warn("config reload failed", "path", cfgPath, "error", err)
				continue
			}

			if overridePort != 0 {
				cfg.Server.Port = overridePort
			}

			rt, err := buildRouter(ctx, cfg)
			if err != nil {
				slog.Warn("provider rebuild failed", "error", err)
				continue
			}

			srv.UpdateRouting(cfg, rt)
			slog.Info("configuration reloaded", "path", cfgPath)
			lastMod = modTime
		}
	}
}
