package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sonhal/nyttig/internal/client"
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
	// Assessment filters: AssessorID selects whose scores MinScore and
	// sort=score use; the daemon rejects them without it.
	AssessorID   int64
	MinScore     *float64
	UnassessedBy int64
	After        *timestamppb.Timestamp // nil = no window
}

// maxAfter is the last second a protobuf Timestamp can hold (9999-12-31).
const maxAfter = 253402300799

// parseAfter reads after=<unix seconds>: a decimal integer from 0 up, or
// absent for no window. The client fixes it once per snapshot (see the web
// app's stream), so the daemon never counts "now" itself.
func parseAfter(v url.Values) (*timestamppb.Timestamp, error) {
	s := v.Get("after")
	if s == "" {
		return nil, nil
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return nil, fmt.Errorf("%w: after must be a unix time in seconds", errBadParam)
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n > maxAfter {
		return nil, fmt.Errorf("%w: after must be a unix time in seconds", errBadParam)
	}
	return timestamppb.New(time.Unix(n, 0)), nil
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
	case "", "newest", "oldest", "score":
		f.Sort = s
	default:
		return f, fmt.Errorf("%w: sort must be newest, oldest or score", errBadParam)
	}
	if f.UnviewedOnly, err = parseBool(v, "unviewed"); err != nil {
		return f, err
	}
	if f.AssessorID, err = parseID(v, "assessor"); err != nil {
		return f, err
	}
	if f.UnassessedBy, err = parseID(v, "unassessed"); err != nil {
		return f, err
	}
	if s := v.Get("min_score"); s != "" {
		x, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(x) || math.IsInf(x, 0) || x < 0 || x > 1 {
			return f, fmt.Errorf("%w: min_score must be a number from 0 to 1", errBadParam)
		}
		f.MinScore = &x
	}
	if f.After, err = parseAfter(v); err != nil {
		return f, err
	}
	return f, nil
}

// maxViewRef bounds the view= parameter (a name or an ID).
const maxViewRef = 200

// errViewNotFound is returned (wrapped) when view= names no saved view.
var errViewNotFound = client.ErrViewNotFound

// feedRequest turns the query of /api/items and /api/stream into the
// SearchRequest-shaped filter the daemon takes. Without view= the parameters
// are the filter. With view=<id or name> the saved view is the base (an ID if
// a view has it, else the name, any case; unknown is errViewNotFound) and a
// parameter that is present replaces the view's field, the same rule as
// `nyttig search -view` (client.ViewSearchRequest does both). A view's window
// ("since:1d") becomes the cutoff now minus that window, taken once here, so
// one request or one stream snapshot shares a single cutoff; an explicit
// after= wins over it. Paging and tag_exact are the caller's.
func feedRequest(ctx context.Context, c pb.NyttigClient, now time.Time, v url.Values) (*pb.SearchRequest, error) {
	f, err := parseFeedFilter(v)
	if err != nil {
		return nil, err
	}
	var view *pb.SavedView
	if ref := v.Get("view"); ref != "" {
		if len(ref) > maxViewRef {
			return nil, fmt.Errorf("%w: view is longer than %d bytes", errBadParam, maxViewRef)
		}
		resp, err := c.ListSavedViews(ctx, &emptypb.Empty{})
		if err != nil {
			return nil, err
		}
		if view, err = client.FindView(resp.Views, ref); err != nil {
			return nil, err
		}
	}
	// A parameter that is present replaces the view's field, and present but
	// empty (or 0 / false) clears it: unviewed=0 drops is:unviewed, min_score=
	// the minimum, assessor= the assessor (the minimum and the score sort go
	// with it), unassessed= its filter, after= the window, sort=newest a score
	// sort. Absent keeps the view's.
	var o client.Overrides
	if v.Has("q") {
		o.Query = &f.Query
	}
	if v.Has("source") {
		o.SourceID = &f.SourceID
	}
	if v.Has("tag") {
		o.TagID = &f.TagID
	}
	if v.Has("sort") {
		o.Sort = &f.Sort
	}
	if v.Has("unviewed") {
		o.UnviewedOnly = &f.UnviewedOnly
	}
	if v.Has("assessor") {
		o.AssessorID = &f.AssessorID
	}
	if v.Has("min_score") {
		o.MinScore = f.MinScore
		o.NoMinScore = f.MinScore == nil
	}
	if v.Has("unassessed") {
		o.UnassessedBy = &f.UnassessedBy
	}
	if v.Has("after") {
		none := ""
		o.Since = &none // an empty after= drops the view's window
	}
	o.After = f.After
	return client.ViewSearchRequest(view, o, now)
}

// writeFeedError answers a failed feedRequest: 400 for a bad parameter, 404
// for an unknown view, and the daemon's error otherwise.
func writeFeedError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errBadParam):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, errViewNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		if _, ok := status.FromError(err); ok {
			writeRPCError(w, err)
			return
		}
		// A view whose stored window the parser rejects.
		writeError(w, http.StatusBadRequest, err.Error())
	}
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
	// now is the clock for a view's window (view=); tests fix it.
	now func() time.Time
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
	// tag=ID matches the tag and every tag below it; tag_exact=1 only the tag.
	tagExact, err := parseBool(v, "tag_exact")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	req, err := feedRequest(ctx, a.client, a.now(), v)
	if err != nil {
		writeFeedError(w, err)
		return
	}
	req.TagExact = tagExact
	req.Limit = int32(limit)
	req.Offset = int32(offset)
	resp, err := a.client.Search(ctx, req)
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
