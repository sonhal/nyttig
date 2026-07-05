// Nyttig daemon — runs continuously, fetching feeds on a schedule and
// serving the gRPC API over a Unix domain socket (or TCP).
//
//	nyttigd --socket /tmp/nyttig.sock
//	nyttigd --socket :9090                        # TCP
//	nyttigd --config ~/.config/nyttig/config.toml
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/sonhal/nyttig/internal/config"
	"github.com/sonhal/nyttig/internal/mtls"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/db"
	"github.com/sonhal/nyttig/internal/server/fetcher"
	"github.com/sonhal/nyttig/internal/server/scheduler"
	"github.com/sonhal/nyttig/internal/server/service"
	"github.com/sonhal/nyttig/internal/server/tagger"
)

func main() {
	var (
		socket      = flag.String("socket", "/tmp/nyttig.sock", "Unix socket path or TCP address to listen on")
		dbPath      = flag.String("db-path", "", "Path to SQLite database (default: ~/.local/share/nyttig/nyttig.db)")
		logLevel    = flag.String("log-level", "info", "Log level: debug, info, warn, error")
		configPath  = flag.String("config", "", "Path to TOML config file (seeds sources/tags/rules; provides defaults for socket, db-path, log-level)")
		tlsCert     = flag.String("tls-cert", "", "Server TLS certificate (PEM); enables mTLS together with -tls-key and -tls-client-ca")
		tlsKey      = flag.String("tls-key", "", "Server TLS private key (PEM)")
		tlsClientCA = flag.String("tls-client-ca", "", "CA bundle (PEM) used to verify client certificates")
	)
	flag.Parse()

	// Track which flags were explicitly passed, so config values only act as
	// defaults: an explicit command-line flag always wins over the config file.
	setFlags := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	// ── Config file ──────────────────────────────────────────────────────
	// Loaded before logging is configured because it can override log-level.
	var cfg *config.Config
	if *configPath != "" {
		c, err := config.Load(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cannot load config: %v\n", err)
			os.Exit(1)
		}
		cfg = c
		if cfg.Socket != "" && !setFlags["socket"] {
			*socket = cfg.Socket
		}
		if cfg.DBPath != "" && !setFlags["db-path"] {
			*dbPath = cfg.DBPath
		}
		if cfg.LogLevel != "" && !setFlags["log-level"] {
			*logLevel = cfg.LogLevel
		}
		if cfg.TLS.Cert != "" && !setFlags["tls-cert"] {
			*tlsCert = cfg.TLS.Cert
		}
		if cfg.TLS.Key != "" && !setFlags["tls-key"] {
			*tlsKey = cfg.TLS.Key
		}
		if cfg.TLS.ClientCA != "" && !setFlags["tls-client-ca"] {
			*tlsClientCA = cfg.TLS.ClientCA
		}
	}

	// ── Logging ──────────────────────────────────────────────────────────
	var level slog.Level
	switch *logLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	// ── Database ─────────────────────────────────────────────────────────
	dsn := config.ExpandHome(*dbPath)
	if dsn == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			logger.Error("cannot determine home directory", "error", err)
			os.Exit(1)
		}
		dsn = home + "/.local/share/nyttig/nyttig.db"
	}

	// Ensure directory exists.
	if dir := dirOf(dsn); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			logger.Error("cannot create database directory", "dir", dir, "error", err)
			os.Exit(1)
		}
	}

	database, err := db.Open(dsn + "?_journal_mode=WAL&_foreign_keys=ON&_busy_timeout=5000")
	if err != nil {
		logger.Error("cannot open database", "dsn", dsn, "error", err)
		os.Exit(1)
	}
	defer database.Close()
	logger.Info("database opened", "dsn", dsn)

	// ── Seed from config ─────────────────────────────────────────────────
	// Idempotently insert sources/tags/tag_rules declared in the config file.
	// Existing rows (matched by URL / name / rule tuple) are left untouched.
	if cfg != nil {
		if err := seedFromConfig(database, cfg, logger); err != nil {
			logger.Error("cannot seed from config", "error", err)
			os.Exit(1)
		}
	}

	// ── gRPC service ─────────────────────────────────────────────────────
	svc := service.New(database)
	hub := svc.Hub()

	// ── Pipeline: fetch → tag → push to Hub ─────────────────────────────
	taggerStore := &dbTaggerStore{db: database}
	tgr := tagger.New(taggerStore, logger)

	// Scheduler adapter: converts between db.Source and scheduler.Source.
	srcStore := &dbSourceStore{db: database}

	sched := scheduler.New(srcStore, nil, nil, logger) // fetchFn and clock set below

	// fetchFn is the callback the scheduler invokes for each source fetch.
	fetchFn := func(ctx context.Context, s scheduler.Source) error {
		return doFetch(ctx, database, s, tgr, hub, logger)
	}

	// Inject fetchFn into the scheduler.
	sched.SetFetchFn(fetchFn)

	// Wire RefreshSource RPC to the scheduler.
	svc.SetRefreshSourceFunc(func(ctx context.Context, sourceID int64) error {
		if sourceID == 0 {
			// Refresh all enabled sources.
			sources, err := db.ListEnabledSources(database)
			if err != nil {
				return fmt.Errorf("list enabled sources: %w", err)
			}
			for _, src := range sources {
				if err := sched.RefreshSource(src.ID); err != nil {
					logger.Warn("refresh source failed", "source_id", src.ID, "error", err)
					continue
				}
			}
			return nil
		}
		return sched.RefreshSource(sourceID)
	})

	// Wire source lifecycle callbacks to the scheduler.
	svc.OnSourceAdded(func(ctx context.Context, src db.Source) {
		sched.AddSource(ctx, dbSourceToSchedulerSource(&src))
	})
	svc.OnSourceRemoved(func(id int64) {
		sched.RemoveSource(id)
	})
	svc.OnSourceUpdated(func(ctx context.Context, src db.Source) {
		if src.Enabled {
			sched.EnableSource(ctx, src.ID)
		} else {
			sched.DisableSource(src.ID)
		}
	})

	// Start the scheduler (immediate first fetch for all enabled sources).
	if err := sched.Start(context.Background()); err != nil {
		logger.Error("cannot start scheduler", "error", err)
		os.Exit(1)
	}
	logger.Info("scheduler started")

	// ── gRPC server ──────────────────────────────────────────────────────
	grpcOpts := []grpc.ServerOption{
		grpc.MaxConcurrentStreams(100),
	}

	// Configure mutual TLS if any TLS option was provided (flag or config).
	// All three are required together; mtls.ServerCredentials enforces this.
	tlsOpts := mtls.ServerOptions{
		CertFile:     config.ExpandHome(*tlsCert),
		KeyFile:      config.ExpandHome(*tlsKey),
		ClientCAFile: config.ExpandHome(*tlsClientCA),
	}
	if tlsOpts.Enabled() {
		creds, err := mtls.ServerCredentials(tlsOpts)
		if err != nil {
			logger.Error("cannot configure mTLS", "error", err)
			os.Exit(1)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(creds))
		logger.Info("mutual TLS enabled", "cert", tlsOpts.CertFile, "client_ca", tlsOpts.ClientCAFile)
	} else {
		logger.Warn("TLS not configured; serving in plaintext (use only on a local socket or trusted network)")
	}

	grpcServer := grpc.NewServer(grpcOpts...)
	pb.RegisterNyttigServer(grpcServer, svc)
	reflection.Register(grpcServer)

	// ── Listener ─────────────────────────────────────────────────────────
	var lis net.Listener
	addr := *socket
	if addr[0] == ':' || isHostPort(addr) {
		// TCP listener.
		lis, err = net.Listen("tcp", addr)
	} else {
		// Unix domain socket.
		os.Remove(addr) // Clean up stale socket.
		lis, err = net.Listen("unix", addr)
	}
	if err != nil {
		logger.Error("cannot listen", "addr", addr, "error", err)
		os.Exit(1)
	}
	defer lis.Close()
	logger.Info("gRPC server listening", "addr", addr)

	// ── Signal handling / graceful shutdown ──────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine.
	serverErr := make(chan error, 1)
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			serverErr <- err
		}
	}()

	logger.Info("nyttigd started", "version", "0.1.0")

	// Wait for shutdown signal or server error.
	select {
	case sig := <-sigCh:
		logger.Info("shutting down", "signal", sig.String())
	case err := <-serverErr:
		logger.Error("server error", "error", err)
	}

	// Stop the scheduler (cancels all source goroutines).
	sched.Stop()
	logger.Info("scheduler stopped")

	// Graceful shutdown with timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("server stopped gracefully")
	case <-ctx.Done():
		logger.Warn("graceful stop timed out, forcing")
		grpcServer.Stop()
	}
}

// ── Config seeding ───────────────────────────────────────────────────────────

// seedFromConfig idempotently inserts the sources, tags, and tag rules declared
// in cfg. Sources are matched by URL, tags by name, and tag rules by their
// (tag, field, pattern, source) tuple; anything already present is skipped so
// the config file never clobbers user edits or fetch state on restart.
func seedFromConfig(database *sql.DB, cfg *config.Config, logger *slog.Logger) error {
	// ── Sources ──
	existingSources, err := db.ListSources(database)
	if err != nil {
		return fmt.Errorf("list sources: %w", err)
	}
	srcIDByURL := make(map[string]int64)
	srcIDByName := make(map[string]int64)
	for _, s := range existingSources {
		srcIDByURL[s.URL] = s.ID
		srcIDByName[s.Name] = s.ID
	}
	for _, cs := range cfg.Sources {
		if cs.URL == "" {
			logger.Warn("skipping config source with empty url", "name", cs.Name)
			continue
		}
		if _, ok := srcIDByURL[cs.URL]; ok {
			continue
		}
		typ := cs.Type
		if typ == "" {
			typ = "rss"
		}
		refresh := cs.RefreshSec
		if refresh == 0 {
			refresh = 3600
		}
		var color *string
		if cs.Color != "" {
			c := cs.Color
			color = &c
		}
		var abbreviation *string
		if cs.Abbreviation != "" {
			a := cs.Abbreviation
			abbreviation = &a
		}
		id, err := db.InsertSource(database, &db.Source{
			Name: cs.Name, URL: cs.URL, Type: typ, RefreshSec: refresh, Enabled: true,
			Color: color, Abbreviation: abbreviation,
		})
		if err != nil {
			return fmt.Errorf("insert source %q: %w", cs.URL, err)
		}
		srcIDByURL[cs.URL] = id
		srcIDByName[cs.Name] = id
		logger.Info("seeded source from config", "name", cs.Name, "url", cs.URL)
	}

	// ── Tags ──
	existingTags, err := db.ListTags(database)
	if err != nil {
		return fmt.Errorf("list tags: %w", err)
	}
	tagIDByName := make(map[string]int64)
	for _, t := range existingTags {
		tagIDByName[t.Name] = t.ID
	}
	for _, ct := range cfg.Tags {
		if ct.Name == "" {
			continue
		}
		if _, ok := tagIDByName[ct.Name]; ok {
			continue
		}
		var color *string
		if ct.Color != "" {
			c := ct.Color
			color = &c
		}
		id, err := db.InsertTag(database, ct.Name, color)
		if err != nil {
			return fmt.Errorf("insert tag %q: %w", ct.Name, err)
		}
		tagIDByName[ct.Name] = id
		logger.Info("seeded tag from config", "name", ct.Name)
	}

	// ── Tag rules ──
	existingRules, err := db.ListTagRules(database, nil)
	if err != nil {
		return fmt.Errorf("list tag rules: %w", err)
	}
	ruleKey := func(tagID int64, field, pattern string, sourceID *int64) string {
		src := "global"
		if sourceID != nil {
			src = fmt.Sprintf("%d", *sourceID)
		}
		return fmt.Sprintf("%d|%s|%s|%s", tagID, field, pattern, src)
	}
	haveRule := make(map[string]bool)
	for _, r := range existingRules {
		haveRule[ruleKey(r.TagID, r.Field, r.Pattern, r.SourceID)] = true
	}
	for _, cr := range cfg.TagRules {
		if cr.Tag == "" || cr.Pattern == "" {
			logger.Warn("skipping config tag rule with empty tag or pattern", "tag", cr.Tag)
			continue
		}
		tagID, ok := tagIDByName[cr.Tag]
		if !ok {
			// Rule references a tag not declared in [[tags]]; create it.
			id, err := db.InsertTag(database, cr.Tag, nil)
			if err != nil {
				return fmt.Errorf("insert tag %q for rule: %w", cr.Tag, err)
			}
			tagID = id
			tagIDByName[cr.Tag] = id
		}
		field := cr.Field
		if field == "" {
			field = "both"
		}
		var sourceID *int64
		if cr.Source != "" {
			id, ok := srcIDByName[cr.Source]
			if !ok {
				logger.Warn("tag rule references unknown source; treating as global",
					"tag", cr.Tag, "source", cr.Source)
			} else {
				sourceID = &id
			}
		}
		key := ruleKey(tagID, field, cr.Pattern, sourceID)
		if haveRule[key] {
			continue
		}
		if _, err := db.InsertTagRule(database, &db.TagRule{
			SourceID: sourceID, TagID: tagID, Field: field, Pattern: cr.Pattern, Priority: cr.Priority,
		}); err != nil {
			return fmt.Errorf("insert tag rule for %q: %w", cr.Tag, err)
		}
		haveRule[key] = true
		logger.Info("seeded tag rule from config", "tag", cr.Tag, "pattern", cr.Pattern)
	}

	return nil
}

// ── Adapters ───────────────────────────────────────────────────────────────

// dbSourceStore implements scheduler.SourceStore by reading from the DB.
type dbSourceStore struct {
	db *sql.DB
}

func (s *dbSourceStore) ListEnabledSources() ([]scheduler.Source, error) {
	rows, err := db.ListEnabledSources(s.db)
	if err != nil {
		return nil, err
	}
	out := make([]scheduler.Source, len(rows))
	for i, r := range rows {
		out[i] = dbSourceToSchedulerSource(r)
	}
	return out, nil
}

func (s *dbSourceStore) GetSource(id int64) (scheduler.Source, error) {
	src, err := db.GetSource(s.db, id)
	if err != nil {
		return scheduler.Source{}, err
	}
	if src == nil {
		return scheduler.Source{}, fmt.Errorf("source %d not found", id)
	}
	return dbSourceToSchedulerSource(src), nil
}

// dbTaggerStore implements tagger.RuleStore on top of the db package.
type dbTaggerStore struct {
	db *sql.DB
}

func (s *dbTaggerStore) LoadRules() ([]tagger.TagRule, error) {
	rules, err := db.ListTagRules(s.db, nil)
	if err != nil {
		return nil, err
	}
	out := make([]tagger.TagRule, len(rules))
	for i, r := range rules {
		out[i] = dbTagRuleToTaggerRule(r)
	}
	return out, nil
}

func (s *dbTaggerStore) AssignTag(itemID, tagID int64) error {
	return db.AssignTagToItem(s.db, itemID, tagID)
}

// ── Pipeline helper ────────────────────────────────────────────────────────

// doFetch runs the full fetch→tag→push pipeline for a single source.
func doFetch(
	ctx context.Context,
	database *sql.DB,
	s scheduler.Source,
	tgr *tagger.Tagger,
	hub *service.Hub,
	logger *slog.Logger,
) error {
	// Convert scheduler.Source back to db.Source for the fetcher.
	dbSrc := schedulerSourceToDBSource(s)

	// 1. Fetch and parse the feed.
	result, err := fetcher.Fetch(database, dbSrc)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}

	// 2. Tag newly inserted items.
	taggerItems := make([]tagger.Item, 0, len(result.NewItems))
	for _, item := range result.NewItems {
		taggerItems = append(taggerItems, itemToTaggerItem(item))
	}
	if _, err := tgr.TagItems(taggerItems); err != nil {
		logger.Warn("tag items failed", "error", err)
	}

	// 3. Reload each new item (to include tags, view state) and push to Hub.
	for _, item := range result.NewItems {
		full, err := db.GetItem(database, item.ID)
		if err != nil {
			logger.Warn("reload item failed", "item_id", item.ID, "error", err)
			continue
		}
		if full == nil {
			continue
		}
		hub.Push(service.ItemToProto(full))
	}

	logger.Info("fetch complete",
		"source_id", s.ID,
		"source_name", s.Name,
		"new_items", len(result.NewItems),
		"fetch_error", result.FetchError,
	)

	return nil
}

// ── Conversion helpers ─────────────────────────────────────────────────────

func dbSourceToSchedulerSource(src *db.Source) scheduler.Source {
	return scheduler.Source{
		ID:         src.ID,
		Name:       src.Name,
		URL:        src.URL,
		RefreshSec: int64(src.RefreshSec),
		Enabled:    src.Enabled,
	}
}

func schedulerSourceToDBSource(s scheduler.Source) *db.Source {
	return &db.Source{
		ID:         s.ID,
		Name:       s.Name,
		URL:        s.URL,
		RefreshSec: int(s.RefreshSec),
		Enabled:    s.Enabled,
	}
}

func itemToTaggerItem(item *db.Item) tagger.Item {
	ti := tagger.Item{
		ID:       item.ID,
		SourceID: item.SourceID,
		Title:    item.Title,
	}
	if item.Description != nil {
		ti.Description = *item.Description
	}
	return ti
}

func dbTagRuleToTaggerRule(r *db.TagRule) tagger.TagRule {
	tr := tagger.TagRule{
		ID:       r.ID,
		TagID:    r.TagID,
		TagName:  r.TagName,
		Field:    r.Field,
		Pattern:  r.Pattern,
		Priority: int64(r.Priority),
	}
	if r.SourceID != nil {
		tr.SourceID = *r.SourceID
	}
	return tr
}

// dirOf returns the directory portion of a path.
func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return ""
}

// isHostPort returns true if addr looks like "host:port" (a TCP address).
func isHostPort(addr string) bool {
	for i := 0; i < len(addr); i++ {
		if addr[i] == ':' {
			return i > 0 || addr != ":0"
		}
	}
	return false
}
