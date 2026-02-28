package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

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

	registry := provider.NewRegistry()
	if err := providerfactory.RegisterConfiguredProviders(ctx, cfg, registry); err != nil {
		return err
	}

	rt := router.New(registry)

	srv, err := server.New(cfg, rt)
	if err != nil {
		return err
	}

	return srv.Run(ctx)
}
