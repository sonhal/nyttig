package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// ── Limits ────────────────────────────────────────────────────

const (
	// rpcTimeout bounds every unary call to the daemon.
	rpcTimeout = 10 * time.Second
	// maxBodyBytes caps request bodies.
	maxBodyBytes = 64 << 10
	// maxViewedIDs caps one MarkViewed batch.
	maxViewedIDs = 1000
	// maxQueryLen caps the free-text search query.
	maxQueryLen = 500
	// maxSearchLimit caps one page of /api/items.
	maxSearchLimit = 500
)

// marshaler renders protobuf messages the way the browser client expects:
// proto field names, and zero values left out (the client applies defaults).
var marshaler = protojson.MarshalOptions{UseProtoNames: true}

// ── Responses ─────────────────────────────────────────────────

func writeJSONBytes(w http.ResponseWriter, code int, b []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func writeProto(w http.ResponseWriter, m proto.Message) {
	writeProtoStatus(w, http.StatusOK, m)
}

func writeProtoStatus(w http.ResponseWriter, code int, m proto.Message) {
	b, err := marshaler.Marshal(m)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode response")
		return
	}
	writeJSONBytes(w, code, b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encode response")
		return
	}
	writeJSONBytes(w, code, b)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	b, _ := json.Marshal(map[string]string{"error": msg})
	writeJSONBytes(w, code, b)
}

// writeRPCError maps a daemon error to an HTTP status.
func writeRPCError(w http.ResponseWriter, err error) {
	st, _ := status.FromError(err)
	code := http.StatusBadGateway
	switch st.Code() {
	case codes.InvalidArgument, codes.OutOfRange, codes.FailedPrecondition:
		code = http.StatusBadRequest
	case codes.NotFound:
		code = http.StatusNotFound
	case codes.AlreadyExists:
		code = http.StatusConflict
	case codes.Unavailable:
		code = http.StatusServiceUnavailable
	case codes.DeadlineExceeded:
		code = http.StatusGatewayTimeout
	case codes.Canceled:
		// The browser went away; the status is never seen.
		code = 499
	}
	writeError(w, code, st.Message())
}

// ── Query parsing ─────────────────────────────────────────────

// errBadParam is returned (wrapped) for malformed query parameters.
var errBadParam = errors.New("bad parameter")

func parseID(v url.Values, key string) (int64, error) {
	s := v.Get(key)
	if s == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer", errBadParam, key)
	}
	return id, nil
}

func parseBool(v url.Values, key string) (bool, error) {
	switch v.Get(key) {
	case "", "0", "false":
		return false, nil
	case "1", "true":
		return true, nil
	}
	return false, fmt.Errorf("%w: %s must be true or false", errBadParam, key)
}

// feedFilter is the filter shared by /api/items and /api/stream.
type feedFilter struct {
	Query        string
	SourceID     int64
	TagID        int64
	Sort         string
	UnviewedOnly bool
}

func parseFeedFilter(v url.Values) (feedFilter, error) {
	var f feedFilter
	var err error
	f.Query = v.Get("q")
	if len(f.Query) > maxQueryLen {
		return f, fmt.Errorf("%w: q is longer than %d bytes", errBadParam, maxQueryLen)
	}
	if f.SourceID, err = parseID(v, "source"); err != nil {
		return f, err
	}
	if f.TagID, err = parseID(v, "tag"); err != nil {
		return f, err
	}
	switch s := v.Get("sort"); s {
	case "", "newest", "oldest":
		f.Sort = s
	default:
		return f, fmt.Errorf("%w: sort must be newest or oldest", errBadParam)
	}
	if f.UnviewedOnly, err = parseBool(v, "unviewed"); err != nil {
		return f, err
	}
	return f, nil
}

func parseIntParam(v url.Values, key string, def, lo, hi int) (int, error) {
	s := v.Get(key)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%w: %s must be an integer from %d to %d", errBadParam, key, lo, hi)
	}
	return n, nil
}

// ── Handlers ──────────────────────────────────────────────────

// api holds the JSON handlers. Every handler talks to the daemon only
// through the pb.NyttigClient interface.
type handlers struct {
	client pb.NyttigClient
}

func (a *handlers) rpcContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), rpcTimeout)
}

// health reports whether the daemon answers.
func (a *handlers) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if _, err := a.client.ListTags(ctx, &emptypb.Empty{}); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": status.Convert(err).Message()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *handlers) listSources(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	resp, err := a.client.ListSources(ctx, &emptypb.Empty{})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	for _, s := range resp.Sources {
		sanitizeSource(s)
	}
	writeProto(w, resp)
}

func (a *handlers) listTags(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	resp, err := a.client.ListTags(ctx, &emptypb.Empty{})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	for _, t := range resp.Tags {
		sanitizeTag(t)
	}
	writeProto(w, resp)
}

// refresh triggers a fetch of one source (?source=ID) or of all sources.
func (a *handlers) refresh(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.URL.Query(), "source")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	if _, err := a.client.RefreshSource(ctx, &pb.RefreshSourceRequest{SourceId: id}); err != nil {
		writeRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// items pages through stored items with Search; the client uses it for
// counts and for loading items older than the stream's snapshot.
func (a *handlers) items(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	f, err := parseFeedFilter(v)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := parseIntParam(v, "limit", 100, 1, maxSearchLimit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	offset, err := parseIntParam(v, "offset", 0, 0, 1<<30)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	resp, err := a.client.Search(ctx, &pb.SearchRequest{
		Query:        f.Query,
		SourceId:     f.SourceID,
		TagId:        f.TagID,
		Sort:         f.Sort,
		UnviewedOnly: f.UnviewedOnly,
		Limit:        int32(limit),
		Offset:       int32(offset),
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	for i, it := range resp.Items {
		resp.Items[i] = sanitizeItem(it)
	}
	// Like every zero value, a total of 0 is left out; the client
	// defaults missing fields.
	writeProto(w, resp)
}

// viewedRequest is the body of POST /api/viewed. IDs are strings because
// the client treats int64 IDs as opaque strings (protojson encodes them so).
type viewedRequest struct {
	IDs []string `json:"ids"`
}

func (a *handlers) viewed(w http.ResponseWriter, r *http.Request) {
	var req viewedRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be {\"ids\": [\"1\", ...]}")
		return
	}
	if len(req.IDs) > maxViewedIDs {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("at most %d ids per request", maxViewedIDs))
		return
	}
	ids := make([]int64, 0, len(req.IDs))
	for _, s := range req.IDs {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "ids must be positive integers encoded as strings")
			return
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	if _, err := a.client.MarkViewed(ctx, &pb.MarkViewedRequest{ItemIds: ids}); err != nil {
		writeRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
