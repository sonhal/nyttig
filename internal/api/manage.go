package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// Management endpoints: sources, tags, tag rules and saved views. The daemon validates
// every value (service/validate.go), so these handlers only check the shape
// of a request: exact field names, JSON types, IDs, and which fields are
// present. Presence matters for PATCH, where an absent field is left
// unchanged (the proto3 optional fields of UpdateSourceRequest and
// UpdateTagRequest).

// maxManageBodyBytes caps management request bodies. The largest valid
// body (a source with a 2 KiB URL, or a 1 KiB rule pattern) is far below it.
const maxManageBodyBytes = 16 << 10

// ── Request bodies ────────────────────────────────────────────

// errBody is returned (wrapped) for a malformed request body.
var errBody = errors.New("bad request body")

// jsonBody holds the members of a JSON object request body by exact name.
// Unlike decoding into a struct, it keeps presence (absent vs. zero), and
// field names are matched case-sensitively, as protojson does.
type jsonBody map[string]json.RawMessage

// readBody decodes a JSON object body of at most maxManageBodyBytes whose
// members are all in allowed and none of them null. It writes the error
// response itself and returns false when the body is unacceptable.
func readBody(w http.ResponseWriter, r *http.Request, allowed ...string) (jsonBody, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxManageBodyBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("body must be at most %d bytes", maxManageBodyBytes))
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "read body")
		return nil, false
	}
	b, err := readObject(raw, allowed)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return b, true
}

// readObject reads one JSON object member by member, so that duplicate
// names are caught too (a plain map decode keeps the last one silently).
// An empty body is an empty object.
func readObject(raw []byte, allowed []string) (jsonBody, error) {
	b := jsonBody{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return b, nil
	}
	invalid := fmt.Errorf("%w: must be a JSON object", errBody)
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, invalid
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, invalid
		}
		k, _ := t.(string)
		if !contains(allowed, k) {
			return nil, fmt.Errorf("%w: unknown field %q", errBody, k)
		}
		if b.has(k) {
			return nil, fmt.Errorf("%w: duplicate field %q", errBody, k)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, invalid
		}
		if string(v) == "null" {
			return nil, fmt.Errorf("%w: %s must not be null", errBody, k)
		}
		b[k] = v
	}
	if t, err := dec.Token(); err != nil || t != json.Delim('}') {
		return nil, invalid
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: unexpected data after the JSON object", errBody)
	}
	return b, nil
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}

// has reports whether the body sets field.
func (b jsonBody) has(field string) bool {
	_, ok := b[field]
	return ok
}

// str returns a string field, or nil when it is absent.
func (b jsonBody) str(field string) (*string, error) {
	v, ok := b[field]
	if !ok {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return nil, fmt.Errorf("%w: %s must be a string", errBody, field)
	}
	return &s, nil
}

// boolean returns a boolean field, or nil when it is absent.
func (b jsonBody) boolean(field string) (*bool, error) {
	v, ok := b[field]
	if !ok {
		return nil, nil
	}
	var x bool
	if err := json.Unmarshal(v, &x); err != nil {
		return nil, fmt.Errorf("%w: %s must be true or false", errBody, field)
	}
	return &x, nil
}

// int32Field returns an integer field that fits in an int32, or nil when
// it is absent. Fractions and out-of-range numbers are errors.
func (b jsonBody) int32Field(field string) (*int32, error) {
	v, ok := b[field]
	if !ok {
		return nil, nil
	}
	var x int32
	if err := json.Unmarshal(v, &x); err != nil {
		return nil, fmt.Errorf("%w: %s must be an integer", errBody, field)
	}
	return &x, nil
}

// id returns an ID field, or 0 when it is absent. IDs are int64 and, like
// protojson, the API encodes them as strings; "0" means none.
func (b jsonBody) id(field string) (int64, error) {
	v, ok := b[field]
	if !ok {
		return 0, nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return 0, fmt.Errorf("%w: %s must be an ID string such as \"12\"", errBody, field)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: %s must be an ID string such as \"12\"", errBody, field)
	}
	return n, nil
}

// idList returns an array of ID strings and whether the field is present.
// Each element is validated like id, and must be positive: "0" is no tag.
func (b jsonBody) idList(field string) ([]int64, bool, error) {
	v, ok := b[field]
	if !ok {
		return nil, false, nil
	}
	bad := fmt.Errorf("%w: %s must be an array of ID strings such as [\"12\"]", errBody, field)
	var raw []string
	if err := json.Unmarshal(v, &raw); err != nil {
		return nil, true, bad
	}
	ids := make([]int64, len(raw))
	for i, s := range raw {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			return nil, true, bad
		}
		ids[i] = n
	}
	return ids, true, nil
}

// object returns a nested JSON object member, read with the same strictness
// as the body itself (only the allowed members, no duplicates, no nulls), and
// whether the member is present.
func (b jsonBody) object(field string, allowed ...string) (jsonBody, bool, error) {
	v, ok := b[field]
	if !ok {
		return nil, false, nil
	}
	o, err := readObject(v, allowed)
	if err != nil {
		return nil, true, fmt.Errorf("%w: %s: %s", errBody, field, strings.TrimPrefix(err.Error(), errBody.Error()+": "))
	}
	return o, true, nil
}

// pathID parses the {id} path segment, which must be a positive integer.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

// firstErr returns the first non-nil error.
func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func valueOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// ── Sources ───────────────────────────────────────────────────

var sourceFields = []string{"name", "url", "type", "refresh_sec", "enabled", "color", "abbreviation"}

// addSource creates a source. An absent "enabled" means true: proto3's
// AddSourceRequest.enabled has no presence and defaults to false, which
// would add a source that never fetches.
func (a *handlers) addSource(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r, sourceFields...)
	if !ok {
		return
	}
	name, err1 := b.str("name")
	url, err2 := b.str("url")
	typ, err3 := b.str("type")
	refresh, err4 := b.int32Field("refresh_sec")
	enabled, err5 := b.boolean("enabled")
	color, err6 := b.str("color")
	abbr, err7 := b.str("abbreviation")
	if err := firstErr(err1, err2, err3, err4, err5, err6, err7); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req := &pb.AddSourceRequest{
		Name:         valueOr(name, ""),
		Url:          valueOr(url, ""),
		Type:         valueOr(typ, ""),
		RefreshSec:   valueOr(refresh, 0),
		Enabled:      valueOr(enabled, true),
		Color:        valueOr(color, ""),
		Abbreviation: valueOr(abbr, ""),
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	src, err := a.client.AddSource(ctx, req)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeSource(src)
	writeProtoStatus(w, http.StatusCreated, src)
}

// updateSource patches a source: only the fields in the body change. For
// color and abbreviation, "" clears the value.
func (a *handlers) updateSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, ok := readBody(w, r, sourceFields...)
	if !ok {
		return
	}
	req := &pb.UpdateSourceRequest{Id: id}
	var errs [7]error
	req.Name, errs[0] = b.str("name")
	req.Url, errs[1] = b.str("url")
	req.Type, errs[2] = b.str("type")
	req.RefreshSec, errs[3] = b.int32Field("refresh_sec")
	req.Enabled, errs[4] = b.boolean("enabled")
	req.Color, errs[5] = b.str("color")
	req.Abbreviation, errs[6] = b.str("abbreviation")
	if err := firstErr(errs[:]...); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	src, err := a.client.UpdateSource(ctx, req)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeSource(src)
	writeProto(w, src)
}

// removeSource deletes a source; the daemon deletes its items and its
// source-specific rules with it.
func (a *handlers) removeSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, ok := readBody(w, r); !ok {
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	if _, err := a.client.RemoveSource(ctx, &pb.RemoveSourceRequest{Id: id}); err != nil {
		writeRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Tags ──────────────────────────────────────────────────────

var tagFields = []string{"name", "color", "parent_ids"}

func (a *handlers) addTag(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r, tagFields...)
	if !ok {
		return
	}
	name, err1 := b.str("name")
	color, err2 := b.str("color")
	parents, _, err3 := b.idList("parent_ids")
	if err := firstErr(err1, err2, err3); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	tag, err := a.client.AddTag(ctx, &pb.AddTagRequest{Name: valueOr(name, ""), Color: valueOr(color, ""), ParentIds: parents})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeTag(tag)
	writeProtoStatus(w, http.StatusCreated, tag)
}

// updateTag renames, recolors and/or re-parents a tag in place, keeping its
// rules and item assignments. Absent fields are unchanged; color "" clears
// it; parent_ids replaces the parents, and [] makes the tag top-level.
func (a *handlers) updateTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, ok := readBody(w, r, tagFields...)
	if !ok {
		return
	}
	req := &pb.UpdateTagRequest{Id: id}
	var err1, err2 error
	req.Name, err1 = b.str("name")
	req.Color, err2 = b.str("color")
	parents, present, err3 := b.idList("parent_ids")
	if err := firstErr(err1, err2, err3); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if present {
		req.Parents = &pb.TagParents{Ids: parents}
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	tag, err := a.client.UpdateTag(ctx, req)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeTag(tag)
	writeProto(w, tag)
}

// removeTag deletes a tag; the daemon deletes its rules and item
// assignments with it.
func (a *handlers) removeTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, ok := readBody(w, r); !ok {
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	if _, err := a.client.RemoveTag(ctx, &pb.RemoveTagRequest{Id: id}); err != nil {
		writeRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Tag rules ─────────────────────────────────────────────────

func (a *handlers) listRules(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	resp, err := a.client.ListTagRules(ctx, &emptypb.Empty{})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProto(w, resp)
}

// addRule creates a tag rule. There is no update: the client edits a rule
// by adding the new one and then removing the old one.
func (a *handlers) addRule(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r, "source_id", "tag_id", "field", "pattern", "priority")
	if !ok {
		return
	}
	sourceID, err1 := b.id("source_id")
	tagID, err2 := b.id("tag_id")
	field, err3 := b.str("field")
	pattern, err4 := b.str("pattern")
	priority, err5 := b.int32Field("priority")
	if err := firstErr(err1, err2, err3, err4, err5); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if tagID == 0 {
		writeError(w, http.StatusBadRequest, "tag_id is required")
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	rule, err := a.client.AddTagRule(ctx, &pb.AddTagRuleRequest{
		SourceId: sourceID,
		TagId:    tagID,
		Field:    valueOr(field, ""),
		Pattern:  valueOr(pattern, ""),
		Priority: valueOr(priority, 0),
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeProtoStatus(w, http.StatusCreated, rule)
}

// removeRule deletes a rule. Items it already tagged keep their tags.
func (a *handlers) removeRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, ok := readBody(w, r); !ok {
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	if _, err := a.client.RemoveTagRule(ctx, &pb.RemoveTagRuleRequest{Id: id}); err != nil {
		writeRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testRule dry-runs a pattern with TestTagRule. The daemon matches with Go
// RE2 exactly as the tagger does; the browser never evaluates patterns.
// It is a POST because the pattern can be long, but it changes nothing.
func (a *handlers) testRule(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r, "source_id", "field", "pattern", "limit")
	if !ok {
		return
	}
	sourceID, err1 := b.id("source_id")
	field, err2 := b.str("field")
	pattern, err3 := b.str("pattern")
	limit, err4 := b.int32Field("limit")
	if err := firstErr(err1, err2, err3, err4); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if limit != nil && (*limit < 1 || *limit > 100) {
		writeError(w, http.StatusBadRequest, "limit must be an integer from 1 to 100")
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	resp, err := a.client.TestTagRule(ctx, &pb.TestTagRuleRequest{
		SourceId: sourceID,
		Field:    valueOr(field, ""),
		Pattern:  valueOr(pattern, ""),
		Limit:    valueOr(limit, 0),
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	for i, it := range resp.Items {
		resp.Items[i] = sanitizeItem(it)
	}
	writeProto(w, resp)
}

// ── Saved views ───────────────────────────────────────────────

var (
	viewFields   = []string{"name", "filter", "favorite"}
	filterFields = []string{"q", "source", "tag", "sort", "unviewed"}
)

// viewJSON is a saved view as the browser sees it. Its filter uses the same
// keys as a request body and the web's Filter (and the feed URL), not the
// proto field names; zero values are left out, and IDs are strings.
type viewJSON struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Filter   filterJSON `json:"filter"`
	Favorite bool       `json:"favorite,omitempty"`
	Position int32      `json:"position,omitempty"`
}

type filterJSON struct {
	Q        string `json:"q,omitempty"`
	Source   string `json:"source,omitempty"`
	Tag      string `json:"tag,omitempty"`
	Sort     string `json:"sort,omitempty"`
	Unviewed bool   `json:"unviewed,omitempty"`
}

func idString(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

func toViewJSON(v *pb.SavedView) viewJSON {
	f := v.GetFilter()
	return viewJSON{
		ID:   strconv.FormatInt(v.GetId(), 10),
		Name: v.GetName(),
		Filter: filterJSON{
			Q: f.GetSearch(), Source: idString(f.GetSourceId()), Tag: idString(f.GetTagId()),
			Sort: f.GetSort(), Unviewed: f.GetUnviewedOnly(),
		},
		Favorite: v.GetFavorite(),
		Position: v.GetPosition(),
	}
}

func toViewsJSON(views []*pb.SavedView) map[string][]viewJSON {
	out := make([]viewJSON, len(views))
	for i, v := range views {
		out[i] = toViewJSON(v)
	}
	return map[string][]viewJSON{"views": out}
}

// viewFilter reads the "filter" member, and whether it is present.
func viewFilter(b jsonBody) (*pb.ViewFilter, bool, error) {
	o, present, err := b.object("filter", filterFields...)
	if err != nil || !present {
		return nil, present, err
	}
	q, err1 := o.str("q")
	src, err2 := o.id("source")
	tag, err3 := o.id("tag")
	sort, err4 := o.str("sort")
	unviewed, err5 := o.boolean("unviewed")
	if err := firstErr(err1, err2, err3, err4, err5); err != nil {
		return nil, true, fmt.Errorf("%w: filter: %s", errBody, strings.TrimPrefix(err.Error(), errBody.Error()+": "))
	}
	return &pb.ViewFilter{Search: valueOr(q, ""), SourceId: src, TagId: tag, Sort: valueOr(sort, ""), UnviewedOnly: valueOr(unviewed, false)}, true, nil
}

func (a *handlers) listViews(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	resp, err := a.client.ListSavedViews(ctx, &emptypb.Empty{})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toViewsJSON(resp.Views))
}

// addView creates a view. An absent filter is an unfiltered view.
func (a *handlers) addView(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r, viewFields...)
	if !ok {
		return
	}
	name, err1 := b.str("name")
	filter, _, err2 := viewFilter(b)
	favorite, err3 := b.boolean("favorite")
	if err := firstErr(err1, err2, err3); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	v, err := a.client.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: valueOr(name, ""), Filter: filter, Favorite: valueOr(favorite, false)})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toViewJSON(v))
}

// updateView renames, re-filters and/or (un)favorites a view. Absent fields
// are unchanged; a present filter replaces the whole saved filter.
func (a *handlers) updateView(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, ok := readBody(w, r, viewFields...)
	if !ok {
		return
	}
	req := &pb.UpdateSavedViewRequest{Id: id}
	var err1, err3 error
	req.Name, err1 = b.str("name")
	filter, _, err2 := viewFilter(b)
	req.Filter = filter
	req.Favorite, err3 = b.boolean("favorite")
	if err := firstErr(err1, err2, err3); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	v, err := a.client.UpdateSavedView(ctx, req)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toViewJSON(v))
}

func (a *handlers) removeView(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, ok := readBody(w, r); !ok {
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	if _, err := a.client.RemoveSavedView(ctx, &pb.RemoveSavedViewRequest{Id: id}); err != nil {
		writeRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reorderViews sets the display order. ids must list every view once; the
// daemon checks that. It answers with the views in the new order.
func (a *handlers) reorderViews(w http.ResponseWriter, r *http.Request) {
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
	resp, err := a.client.ReorderSavedViews(ctx, &pb.ReorderSavedViewsRequest{Ids: ids})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toViewsJSON(resp.Views))
}
