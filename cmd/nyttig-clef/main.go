// nyttig-clef scores news items with Cloudflare's Clef decision models and
// writes the scores as nyttig assessments. It is a gRPC client of nyttigd,
// like nyttig-api: it finds its work through a saved view, asks Clef the
// configured questions about each item, and calls PutAssessment.
//
//	nyttig-clef --config /etc/nyttig/clef.toml
//
// See docs/clef-assessor-plan.md and the README's "Clef assessor" section.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sonhal/nyttig/internal/clef"
	"github.com/sonhal/nyttig/internal/client"
	"github.com/sonhal/nyttig/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "nyttig-clef: %v\n", err)
		os.Exit(1)
	}
}

// options are the parsed command-line flags.
type options struct {
	config      string
	once        bool
	dryRun      bool
	logLevel    string
	showVersion bool
}

func parseFlags(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("nyttig-clef", flag.ContinueOnError)
	fs.StringVar(&o.config, "config", "", "Path to the TOML config file (required)")
	fs.BoolVar(&o.once, "once", false, "Drain the view once and exit")
	fs.BoolVar(&o.dryRun, "dry-run", false, "Call Clef and log the assessments that would be written, write nothing")
	fs.StringVar(&o.logLevel, "log-level", "info", "Log level: debug, info, warn, error")
	fs.BoolVar(&o.showVersion, "version", false, "Print the version and exit")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.showVersion {
		return o, nil
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if o.config == "" {
		return o, errors.New("--config is required")
	}
	return o, nil
}

func run(args []string) error {
	o, err := parseFlags(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if o.showVersion {
		fmt.Println(version.String())
		return nil
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(o.logLevel)); err != nil {
		return fmt.Errorf("--log-level: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	cfg, err := clef.Load(o.config)
	if err != nil {
		return err
	}
	token, err := cfg.ReadToken(os.ReadFile, os.Getenv)
	if err != nil {
		return err
	}
	decider, err := clef.NewClient(clef.ClientConfig{AccountID: cfg.AccountID, Model: cfg.Model, Token: token})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	daemon := client.New(client.Options{
		Addr:       cfg.Socket,
		TLSCert:    cfg.TLS.Cert,
		TLSKey:     cfg.TLS.Key,
		TLSCA:      cfg.TLS.CA,
		ServerName: cfg.TLS.ServerName,
	})
	defer func() { _ = daemon.Close() }()

	logger.Info("nyttig-clef starting", "version", version.String(), "daemon", cfg.Socket,
		"model", cfg.Model, "once", o.once, "dry_run", o.dryRun)

	loop := clef.New(clef.Options{
		Config: cfg, Daemon: daemon, Decider: decider, Logger: logger, DryRun: o.dryRun,
	})
	if err := loop.Start(ctx); err != nil {
		return err
	}
	if err := loop.Run(ctx, o.once); err != nil {
		return err
	}
	logger.Info("nyttig-clef stopped")
	return nil
}
