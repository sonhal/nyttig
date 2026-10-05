package api

import (
	"fmt"
	"net/http"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// Digest series and digests; see docs/digests-plan.md. A digest is a document
// an assessor wrote about one to many items, kept in a series. Like the other
// management endpoints these only check the shape of a request; the daemon
// validates values (service/validate.go). IDs are strings, periods are RFC
// 3339 strings, and `items` / `inputs` are arrays of ID strings.
//
// Digests are untrusted text (an LLM's output can repeat markup or
// instructions from the feed it read). They go through sanitizeDigest on the
// way out, and the browser renders the body as text nodes only.

// maxDigestRequestBytes caps the body of a digest request: the daemon's 64 KiB
// text (twice that when every other character is escaped) and up to 1100
// linked IDs.
const maxDigestRequestBytes = 256 << 10

var (
	digestSeriesCreateFields = []string{"assessor", "name", "description"}
	digestSeriesUpdateFields = []string{"name", "description"}
	digestUpdateFields       = []string{"title", "body", "period_start", "period_end", "items", "inputs"}
	digestCreateFields       = append([]string{"series"}, digestUpdateFields...)
)

// timestamp returns an RFC 3339 time field, or nil when it is absent.
func (b jsonBody) timestamp(field string) (*timestamppb.Timestamp, error) {
	s, err := b.str(field)
	if err != nil || s == nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return nil, fmt.Errorf("%w: %s must be an RFC 3339 time such as \"2026-10-05T00:00:00Z\"", errBody, field)
	}
	return timestamppb.New(t), nil
}

// ── Series ────────────────────────────────────────────────────

func (a *handlers) listDigestSeries(w http.ResponseWriter, r *http.Request) {
	assessor, err := parseID(r.URL.Query(), "assessor")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	resp, err := a.client.ListDigestSeries(ctx, &pb.ListDigestSeriesRequest{AssessorId: assessor})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	for _, s := range resp.Series {
		sanitizeDigestSeries(s)
	}
	writeProto(w, resp)
}

func (a *handlers) addDigestSeries(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r, digestSeriesCreateFields...)
	if !ok {
		return
	}
	assessor, err1 := b.id("assessor")
	name, err2 := b.str("name")
	desc, err3 := b.str("description")
	if err := firstErr(err1, err2, err3); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if assessor == 0 {
		writeError(w, http.StatusBadRequest, "assessor is required")
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	s, err := a.client.AddDigestSeries(ctx, &pb.AddDigestSeriesRequest{
		AssessorId: assessor, Name: valueOr(name, ""), Description: valueOr(desc, ""),
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeDigestSeries(s)
	writeProtoStatus(w, http.StatusCreated, s)
}

// updateDigestSeries patches a series: only the fields in the body change,
// and "" clears the description.
func (a *handlers) updateDigestSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, ok := readBody(w, r, digestSeriesUpdateFields...)
	if !ok {
		return
	}
	req := &pb.UpdateDigestSeriesRequest{Id: id}
	var err1, err2 error
	req.Name, err1 = b.str("name")
	req.Description, err2 = b.str("description")
	if err := firstErr(err1, err2); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	s, err := a.client.UpdateDigestSeries(ctx, req)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeDigestSeries(s)
	writeProto(w, s)
}

// removeDigestSeries deletes a series and its digests.
func (a *handlers) removeDigestSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, ok := readBody(w, r); !ok {
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	if _, err := a.client.RemoveDigestSeries(ctx, &pb.RemoveDigestSeriesRequest{Id: id}); err != nil {
		writeRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reorderDigestSeries sets the display order: {"ids": [...]} lists every
// series once.
func (a *handlers) reorderDigestSeries(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r, "ids")
	if !ok {
		return
	}
	ids, present, err := b.idList("ids")
	if err == nil && !present {
		err = fmt.Errorf("%w: ids is required", errBody)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	resp, err := a.client.ReorderDigestSeries(ctx, &pb.ReorderDigestSeriesRequest{Ids: ids})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	for _, s := range resp.Series {
		sanitizeDigestSeries(s)
	}
	writeProto(w, resp)
}

// ── Digests ───────────────────────────────────────────────────

// listDigests pages a series' digests, newest period first:
// ?series=ID[&before=ID][&limit=N][&body=1]. Bodies are left out unless body=1.
func (a *handlers) listDigests(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	series, err1 := parseID(v, "series")
	before, err2 := parseID(v, "before")
	withBody, err3 := parseBool(v, "body")
	limit, err4 := parseIntParam(v, "limit", 20, 1, 100)
	if err := firstErr(err1, err2, err3, err4); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if series == 0 {
		writeError(w, http.StatusBadRequest, "series is required")
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	resp, err := a.client.ListDigests(ctx, &pb.ListDigestsRequest{
		SeriesId: series, BeforeId: before, Limit: int32(limit), IncludeBody: withBody,
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	for _, d := range resp.Digests {
		sanitizeDigest(d)
	}
	writeProto(w, resp)
}

func (a *handlers) getDigest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	d, err := a.client.GetDigest(ctx, &pb.GetDigestRequest{Id: id})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeDigest(d)
	writeProto(w, d)
}

// addDigest creates a digest: {series, title, body, period_start, period_end,
// items?, inputs?}. It always creates a new one.
func (a *handlers) addDigest(w http.ResponseWriter, r *http.Request) {
	b, ok := readBodyMax(w, r, maxDigestRequestBytes, digestCreateFields...)
	if !ok {
		return
	}
	series, err1 := b.id("series")
	title, err2 := b.str("title")
	body, err3 := b.str("body")
	start, err4 := b.timestamp("period_start")
	end, err5 := b.timestamp("period_end")
	items, _, err6 := b.idList("items")
	inputs, _, err7 := b.idList("inputs")
	if err := firstErr(err1, err2, err3, err4, err5, err6, err7); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if series == 0 {
		writeError(w, http.StatusBadRequest, "series is required")
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	d, err := a.client.AddDigest(ctx, &pb.AddDigestRequest{
		SeriesId: series, Title: valueOr(title, ""), Body: valueOr(body, ""),
		PeriodStart: start, PeriodEnd: end, ItemIds: items, InputIds: inputs,
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeDigest(d)
	writeProtoStatus(w, http.StatusCreated, d)
}

// updateDigest patches a digest: only the fields in the body change (an empty
// `items` or `inputs` array removes those links). It overwrites; there are no
// revisions. A digest is not moved to another series.
func (a *handlers) updateDigest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, ok := readBodyMax(w, r, maxDigestRequestBytes, digestUpdateFields...)
	if !ok {
		return
	}
	req := &pb.UpdateDigestRequest{Id: id}
	var errs [6]error
	req.Title, errs[0] = b.str("title")
	req.Body, errs[1] = b.str("body")
	req.PeriodStart, errs[2] = b.timestamp("period_start")
	req.PeriodEnd, errs[3] = b.timestamp("period_end")
	items, itemsPresent, err := b.idList("items")
	errs[4] = err
	inputs, inputsPresent, err := b.idList("inputs")
	errs[5] = err
	if err := firstErr(errs[:]...); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if itemsPresent {
		req.ItemIds = &pb.IDList{Ids: items}
	}
	if inputsPresent {
		req.InputIds = &pb.IDList{Ids: inputs}
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	d, err := a.client.UpdateDigest(ctx, req)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeDigest(d)
	writeProto(w, d)
}

func (a *handlers) removeDigest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, ok := readBody(w, r); !ok {
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	if _, err := a.client.RemoveDigest(ctx, &pb.RemoveDigestRequest{Id: id}); err != nil {
		writeRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
