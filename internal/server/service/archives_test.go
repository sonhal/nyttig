package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/archiver"
	"github.com/sonhal/nyttig/internal/server/archives"
	"github.com/sonhal/nyttig/internal/server/db"
)

// archiveEnv is a service with archiving enabled: a temporary archive_dir and
// a running worker that archives with fn.
type archiveEnv struct {
	svc      *Service
	database *sql.DB
	dir      string
	srcID    int64
}

func newArchiveEnv(t *testing.T, fn archives.ArchiveFunc) *archiveEnv {
	t.Helper()
	svc, database := newTestService(t)
	dir := filepath.Join(t.TempDir(), "archives")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	w := &archives.Worker{
		DB: database, Dir: dir, Client: http.DefaultClient, Archive: fn,
		Limits: archiver.DefaultLimits(), Workers: 2, Logger: discardLogger(),
		OnFinished: svc.PushItemUpdate,
	}
	svc.SetArchives(dir, w.Wake)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &archiveEnv{svc: svc, database: database, dir: dir, srcID: addSource(t, svc, "feed", "https://example.com/feed").Id}
}

// item inserts an item whose link is link.
func (e *archiveEnv) item(t *testing.T, guid, link string) int64 {
	t.Helper()
	id, _, err := db.InsertItem(e.database, &db.Item{SourceID: e.srcID, GUID: guid, Link: link, Title: "T " + guid})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// waitFinished polls until the archive is neither pending nor running.
func (e *archiveEnv) waitFinished(t *testing.T, id int64) *pb.Archive {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		a, err := e.svc.GetArchive(context.Background(), &pb.GetArchiveRequest{Id: id})
		if err != nil {
			t.Fatalf("GetArchive: %v", err)
		}
		if a.Status != db.ArchivePending && a.Status != db.ArchiveRunning {
			return a
		}
		if time.Now().After(deadline) {
			t.Fatalf("archive %d still %s after 5s", id, a.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// fetchPage archives like the real thing would, minus the page building: it
// GETs the URL with the client it is given and stores the body.
func fetchPage(ctx context.Context, c *http.Client, pageURL string, _ archiver.Limits) (*archiver.Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	res := &archiver.Result{ContentType: "text/html; charset=utf-8", FinalURL: pageURL, HTTPStatus: resp.StatusCode, Body: body}
	if resp.Header.Get("X-Missing") != "" {
		res.MissingAssets = 2
	}
	if resp.Header.Get("X-Note") != "" {
		res.Note = resp.Header.Get("X-Note")
	}
	return res, nil
}

// content reads GetArchiveContent through a real gRPC stream and returns the
// chunk sizes and the joined bytes.
func content(t *testing.T, client pb.NyttigClient, id int64) ([]int, []byte, error) {
	t.Helper()
	stream, err := client.GetArchiveContent(context.Background(), &pb.GetArchiveContentRequest{Id: id})
	if err != nil {
		return nil, nil, err
	}
	var sizes []int
	var all bytes.Buffer
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return sizes, all.Bytes(), nil
		}
		if err != nil {
			return sizes, all.Bytes(), err
		}
		sizes = append(sizes, len(chunk.Data))
		all.Write(chunk.Data)
	}
}

func TestArchiveRPCs_Validation(t *testing.T) {
	ctx := context.Background()
	e := newArchiveEnv(t, fetchPage)
	ftp := e.item(t, "ftp", "ftp://example.com/a")
	nolink := e.item(t, "nolink", "")

	_, err := e.svc.ArchiveItem(ctx, &pb.ArchiveItemRequest{ItemId: 0})
	wantCode(t, err, codes.InvalidArgument)
	_, err = e.svc.ArchiveItem(ctx, &pb.ArchiveItemRequest{ItemId: 9999})
	wantCode(t, err, codes.NotFound)
	_, err = e.svc.ArchiveItem(ctx, &pb.ArchiveItemRequest{ItemId: ftp})
	wantCode(t, err, codes.InvalidArgument)
	_, err = e.svc.ArchiveItem(ctx, &pb.ArchiveItemRequest{ItemId: nolink})
	wantCode(t, err, codes.InvalidArgument)

	_, err = e.svc.GetArchive(ctx, &pb.GetArchiveRequest{Id: 9999})
	wantCode(t, err, codes.NotFound)
	_, err = e.svc.GetArchive(ctx, &pb.GetArchiveRequest{Id: 0})
	wantCode(t, err, codes.InvalidArgument)
	_, err = e.svc.RemoveArchive(ctx, &pb.RemoveArchiveRequest{Id: 9999})
	wantCode(t, err, codes.NotFound)

	for _, req := range []*pb.ListArchivesRequest{{Status: "bogus"}, {BeforeId: -1}, {Limit: -1}, {Limit: 201}} {
		_, err = e.svc.ListArchives(ctx, req)
		wantCode(t, err, codes.InvalidArgument)
	}

	client, _ := startGRPC(t, e.svc)
	_, _, err = content(t, client, 9999)
	wantCode(t, err, codes.NotFound)
}

func TestArchiveRPCs_UnavailableWithoutArchiveDir(t *testing.T) {
	ctx := context.Background()
	svc, database := newTestService(t)
	src := addSource(t, svc, "feed", "https://example.com/feed")
	id, _, _ := db.InsertItem(database, &db.Item{SourceID: src.Id, GUID: "a", Link: "https://example.com/a", Title: "a"})

	_, err := svc.ArchiveItem(ctx, &pb.ArchiveItemRequest{ItemId: id})
	wantCode(t, err, codes.Unavailable)
	_, err = svc.RemoveArchive(ctx, &pb.RemoveArchiveRequest{Id: 1})
	wantCode(t, err, codes.Unavailable)
	client, _ := startGRPC(t, svc)
	_, _, err = content(t, client, 1)
	wantCode(t, err, codes.Unavailable)
}

func TestArchiveItem_StatusesEndToEnd(t *testing.T) {
	ctx := context.Background()
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "<html>ok page</html>") })
	mux.HandleFunc("/partial", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Missing", "1")
		w.Header().Set("X-Note", "little text; may be a consent wall or paywall")
		_, _ = io.WriteString(w, "<html>partial</html>")
	})
	page := httptest.NewServer(mux)
	t.Cleanup(page.Close)

	e := newArchiveEnv(t, fetchPage)
	client, _ := startGRPC(t, e.svc)
	okItem := e.item(t, "ok", page.URL+"/ok")
	partialItem := e.item(t, "partial", page.URL+"/partial")
	missingItem := e.item(t, "missing", page.URL+"/nope")

	queued, err := e.svc.ArchiveItem(ctx, &pb.ArchiveItemRequest{ItemId: okItem})
	if err != nil {
		t.Fatal(err)
	}
	if queued.ItemId != okItem || queued.Link != page.URL+"/ok" || queued.Title != "T ok" || queued.SourceName != "feed" {
		t.Fatalf("queued = %v", queued)
	}
	if queued.Status != db.ArchivePending && queued.Status != db.ArchiveRunning {
		t.Fatalf("status right after queueing = %q", queued.Status)
	}

	done := e.waitFinished(t, queued.Id)
	if done.Status != db.ArchiveDone || done.Error != "" || done.HttpStatus != 200 || done.Size <= 0 ||
		done.OriginalSize != int64(len("<html>ok page</html>")) || done.FinalUrl != page.URL+"/ok" ||
		done.ContentType != "text/html; charset=utf-8" || done.FinishedAt == nil || done.StartedAt == nil {
		t.Fatalf("done = %v", done)
	}
	_, body, err := content(t, client, done.Id)
	if err != nil || string(body) != "<html>ok page</html>" {
		t.Fatalf("content = %q, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, archives.FileName(done.Id, done.ContentType))); err != nil {
		t.Fatalf("archive file: %v", err)
	}

	pa, _ := e.svc.ArchiveItem(ctx, &pb.ArchiveItemRequest{ItemId: partialItem})
	partial := e.waitFinished(t, pa.Id)
	if partial.Status != db.ArchivePartial || partial.MissingAssets != 2 || partial.Note == "" {
		t.Fatalf("partial = %v", partial)
	}
	if _, body, err := content(t, client, partial.Id); err != nil || string(body) != "<html>partial</html>" {
		t.Fatalf("partial content = %q, %v", body, err)
	}

	fa, _ := e.svc.ArchiveItem(ctx, &pb.ArchiveItemRequest{ItemId: missingItem})
	failed := e.waitFinished(t, fa.Id)
	if failed.Status != db.ArchiveFailed || failed.Error != "HTTP 404" {
		t.Fatalf("failed = %v", failed)
	}
	_, _, err = content(t, client, failed.Id)
	wantCode(t, err, codes.FailedPrecondition)

	// Item.archive in the list.
	list, err := e.svc.Search(ctx, &pb.SearchRequest{})
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[int64]string{}
	for _, it := range list.Items {
		if it.Archive != nil {
			statuses[it.Id] = it.Archive.Status
		}
	}
	if len(statuses) != 3 || statuses[okItem] != "done" || statuses[partialItem] != "partial" || statuses[missingItem] != "failed" {
		t.Fatalf("Item.archive statuses = %v", statuses)
	}

	// Archiving a failed item again retries it under the same ID.
	mux.HandleFunc("/nope", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "now here") })
	again, err := e.svc.ArchiveItem(ctx, &pb.ArchiveItemRequest{ItemId: missingItem})
	if err != nil || again.Id != failed.Id {
		t.Fatalf("retry = %v, %v; want archive %d", again, err, failed.Id)
	}
	if retried := e.waitFinished(t, again.Id); retried.Status != db.ArchiveDone || retried.Error != "" {
		t.Fatalf("retried = %v", retried)
	}

	// Filters and paging.
	all, err := e.svc.ListArchives(ctx, &pb.ListArchivesRequest{})
	if err != nil || len(all.Archives) != 3 || all.Archives[0].Id < all.Archives[1].Id {
		t.Fatalf("ListArchives = %v, %v", all, err)
	}
	onlyPartial, _ := e.svc.ListArchives(ctx, &pb.ListArchivesRequest{Status: "partial"})
	if len(onlyPartial.Archives) != 1 || onlyPartial.Archives[0].Id != partial.Id {
		t.Fatalf("partial list = %v", onlyPartial)
	}
	first, _ := e.svc.ListArchives(ctx, &pb.ListArchivesRequest{Limit: 2})
	if len(first.Archives) != 2 || !first.HasMore {
		t.Fatalf("first page = %v", first)
	}

	// Remove deletes the row and the file, and clears Item.archive.
	if _, err := e.svc.RemoveArchive(ctx, &pb.RemoveArchiveRequest{Id: done.Id}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, archives.FileName(done.Id, done.ContentType))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file after remove: %v", err)
	}
	_, err = e.svc.GetArchive(ctx, &pb.GetArchiveRequest{Id: done.Id})
	wantCode(t, err, codes.NotFound)
	_, err = e.svc.RemoveArchive(ctx, &pb.RemoveArchiveRequest{Id: done.Id})
	wantCode(t, err, codes.NotFound)
	if it, _ := db.GetItem(e.database, okItem); it.Archive != nil {
		t.Fatalf("Item.Archive after remove = %+v", it.Archive)
	}
}

func TestGetArchiveContent_StreamsLargeFilesIntact(t *testing.T) {
	big := make([]byte, 2<<20+12345) // two full chunks and a short one
	for i := range big {
		big[i] = byte(i*7 + i>>8)
	}
	e := newArchiveEnv(t, func(context.Context, *http.Client, string, archiver.Limits) (*archiver.Result, error) {
		return &archiver.Result{ContentType: "text/html; charset=utf-8", HTTPStatus: 200, Body: big}, nil
	})
	client, _ := startGRPC(t, e.svc)
	a, err := e.svc.ArchiveItem(context.Background(), &pb.ArchiveItemRequest{ItemId: e.item(t, "big", "https://example.com/big")})
	if err != nil {
		t.Fatal(err)
	}
	if done := e.waitFinished(t, a.Id); done.Status != db.ArchiveDone || done.OriginalSize != int64(len(big)) {
		t.Fatalf("done = %v", done)
	}

	sizes, got, err := content(t, client, a.Id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, big) {
		t.Fatalf("content differs: got %d bytes, want %d", len(got), len(big))
	}
	if len(sizes) != 3 || sizes[0] != 1<<20 || sizes[1] != 1<<20 || sizes[2] != 12345 {
		t.Fatalf("chunk sizes = %v", sizes)
	}
}

func TestArchiveItem_PushesOnQueueAndOnFinish(t *testing.T) {
	release := make(chan struct{})
	e := newArchiveEnv(t, func(ctx context.Context, _ *http.Client, _ string, _ archiver.Limits) (*archiver.Result, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &archiver.Result{ContentType: "text/html; charset=utf-8", HTTPStatus: 200, Body: []byte("x")}, nil
	})
	client, _ := startGRPC(t, e.svc)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream := openStream(t, ctx, client)

	itemID := e.item(t, "pushed", "https://example.com/pushed")
	// The item was inserted behind the service's back; that sends nothing.
	if _, err := e.svc.ArchiveItem(ctx, &pb.ArchiveItemRequest{ItemId: itemID}); err != nil {
		t.Fatal(err)
	}
	recvUpdate := func() *pb.Item {
		t.Helper()
		msg, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		u := msg.GetItemUpdate()
		if u == nil {
			t.Fatalf("message = %v, want an item_update", msg)
		}
		return u
	}
	if u := recvUpdate(); u.Id != itemID || u.Archive == nil || (u.Archive.Status != "pending" && u.Archive.Status != "running") {
		t.Fatalf("update on queue = %v", u)
	}
	// Nothing is pushed while it runs.
	close(release)
	if u := recvUpdate(); u.Id != itemID || u.Archive == nil || u.Archive.Status != "done" {
		t.Fatalf("update on finish = %v", u)
	}

	// Removing clears the archive on the row.
	if _, err := e.svc.RemoveArchive(ctx, &pb.RemoveArchiveRequest{Id: mustArchiveID(t, e.database, itemID)}); err != nil {
		t.Fatal(err)
	}
	if u := recvUpdate(); u.Id != itemID || u.Archive != nil {
		t.Fatalf("update on remove = %v", u)
	}
}

func mustArchiveID(t *testing.T, database *sql.DB, itemID int64) int64 {
	t.Helper()
	it, err := db.GetItem(database, itemID)
	if err != nil || it.Archive == nil {
		t.Fatalf("item %d archive = %+v, %v", itemID, it, err)
	}
	return it.Archive.ID
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
