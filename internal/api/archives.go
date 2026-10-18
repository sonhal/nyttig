package api

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// Archives. An archive is a saved copy of the page behind an item's link:
// untrusted HTML (or a PDF) from the internet. Its metadata (title, error,
// note, link) is untrusted text and goes through sanitizeArchive. The file
// is never shown on the app's origin: archiveFile serves it only as a
// download, with headers that keep a browser from ever rendering it.

// downloadTimeout bounds one file download from the daemon.
const downloadTimeout = 5 * time.Minute

func sanitizeArchive(ar *pb.Archive) {
	ar.Title = safeText(ar.Title)
	ar.SourceName = safeText(ar.SourceName)
	ar.Error = safeText(ar.Error)
	ar.Note = safeText(ar.Note)
	ar.Link = safeLink(ar.Link)
	ar.FinalUrl = safeLink(ar.FinalUrl)
}

// archiveItem queues an item for archiving (or retries a finished one) and
// answers with the archive. The body must be empty or {}.
func (a *handlers) archiveItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, ok := readBody(w, r); !ok {
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	ar, err := a.client.ArchiveItem(ctx, &pb.ArchiveItemRequest{ItemId: id})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeArchive(ar)
	writeProtoStatus(w, http.StatusAccepted, ar)
}

// listArchives pages archives, newest first: ?status=S[&before=ID][&limit=N].
func (a *handlers) listArchives(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	before, err1 := parseID(v, "before")
	limit, err2 := parseIntParam(v, "limit", 50, 1, 200)
	if err := firstErr(err1, err2); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	st := v.Get("status")
	switch st {
	case "", "pending", "running", "done", "partial", "failed":
	default:
		writeError(w, http.StatusBadRequest, "status must be pending, running, done, partial or failed")
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	resp, err := a.client.ListArchives(ctx, &pb.ListArchivesRequest{Status: st, BeforeId: before, Limit: int32(limit)})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	for _, ar := range resp.Archives {
		sanitizeArchive(ar)
	}
	writeProto(w, resp)
}

// getArchive returns one archive's metadata (the expanded feed row shows its
// error and note).
func (a *handlers) getArchive(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	ar, err := a.client.GetArchive(ctx, &pb.GetArchiveRequest{Id: id})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeArchive(ar)
	writeProto(w, ar)
}

func (a *handlers) removeArchive(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, ok := readBody(w, r); !ok {
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	if _, err := a.client.RemoveArchive(ctx, &pb.RemoveArchiveRequest{Id: id}); err != nil {
		writeRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// archiveFile streams the saved page as a download. The headers are a
// security boundary: the body is attacker-controlled HTML, so it is an opaque
// attachment that a browser neither renders nor sniffs, and whose CSP
// sandboxes it even if something opens it.
func (a *handlers) archiveFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), downloadTimeout)
	defer cancel()
	ar, err := a.client.GetArchive(ctx, &pb.GetArchiveRequest{Id: id})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	stream, err := a.client.GetArchiveContent(ctx, &pb.GetArchiveContentRequest{Id: id})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	// The first chunk surfaces errors (not done, file gone) while a JSON
	// error can still be sent.
	chunk, err := stream.Recv()
	if err != nil && !errors.Is(err, io.EOF) {
		writeRPCError(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": archiveFileName(ar)}))
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	for err == nil {
		if _, werr := w.Write(chunk.GetData()); werr != nil {
			return
		}
		chunk, err = stream.Recv()
	}
	// A failure after the first chunk can only cut the body short.
}

// archiveFileName is a safe download name: an ASCII slug of the title with
// the extension of the content type, never a path.
func archiveFileName(ar *pb.Archive) string {
	ext := ".html"
	if mt, _, _ := mime.ParseMediaType(ar.ContentType); mt == "application/pdf" {
		ext = ".pdf"
	}
	var b strings.Builder
	dash := true
	for _, r := range strings.ToLower(ar.Title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 80 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "archive-" + strconv.FormatInt(ar.Id, 10)
	}
	return slug + ext
}
