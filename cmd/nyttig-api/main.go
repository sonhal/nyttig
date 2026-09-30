// nyttig-api serves the nyttig browser client. It is a gRPC client of
// nyttigd, like the TUI, and is meant to run behind a reverse proxy (Caddy)
// that terminates TLS and does authentication.
//
//	nyttig-api --origin https://nyttig.example.com --socket /run/nyttig/nyttig.sock
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sonhal/nyttig/internal/api"
	"github.com/sonhal/nyttig/internal/client"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "nyttig-api: %v\n", err)
		os.Exit(1)
	}
}

// options are the parsed command-line flags.
type options struct {
	listen            string
	allowPublicListen bool
	origin            string
	logLevel          string
	client            client.Options
}

func parseFlags(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("nyttig-api", flag.ContinueOnError)
	fs.StringVar(&o.listen, "listen", "127.0.0.1:7070", "HTTP listen address (host:port); must be loopback unless --allow-public-listen")
	fs.BoolVar(&o.allowPublicListen, "allow-public-listen", false, "Allow a non-loopback --listen address (nyttig-api has no authentication of its own)")
	fs.StringVar(&o.origin, "origin", "", "Public origin the app is served from, e.g. https://nyttig.example.com (required)")
	fs.StringVar(&o.logLevel, "log-level", "info", "Log level: debug, info, warn, error")
	fs.StringVar(&o.client.Addr, "socket", "/tmp/nyttig.sock", "Daemon Unix socket path or TCP address (host:port)")
	fs.StringVar(&o.client.TLSCert, "tls-cert", "", "Client TLS certificate (PEM); enables mTLS together with -tls-key and -tls-ca")
	fs.StringVar(&o.client.TLSKey, "tls-key", "", "Client TLS private key (PEM)")
	fs.StringVar(&o.client.TLSCA, "tls-ca", "", "CA bundle (PEM) used to verify the daemon's certificate")
	fs.StringVar(&o.client.ServerName, "tls-server-name", "", "Override the name verified against the daemon's certificate")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if o.origin == "" {
		return o, errors.New("--origin is required (the URL the browser uses, e.g. https://nyttig.example.com)")
	}
	if _, _, err := api.ParseOrigin(o.origin); err != nil {
		return o, fmt.Errorf("--origin: %w", err)
	}
	if err := api.CheckListenAddr(o.listen, o.allowPublicListen); err != nil {
		return o, err
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

	var level slog.Level
	if err := level.UnmarshalText([]byte(o.logLevel)); err != nil {
		return fmt.Errorf("--log-level: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The connection is lazy and reconnects by itself, so nyttig-api starts
	// (and reports "disconnected") while nyttigd is down.
	conn, err := client.DialConn(ctx, o.client)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	handler, err := api.New(api.Config{
		Client: pb.NewNyttigClient(conn),
		Origin: o.origin,
		Logger: logger,
	})
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", o.listen)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	// No ReadTimeout/WriteTimeout: they would cut off long-lived SSE
	// connections. Request bodies are size-capped and SSE writes carry
	// their own deadline instead.

	logger.Info("nyttig-api listening", "addr", ln.Addr().String(), "origin", o.origin, "daemon", o.client.Addr)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Open SSE streams end when ctx (their BaseContext) is cancelled.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return nil
}
