package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// Assessors and assessments. An assessor is a program (or you) that scores
// items; see docs/assessments-plan.md. Like the other management endpoints,
// these only check the shape of a request: the daemon validates values
// (service/validate.go). Assessor and tag are ID strings, as everywhere in
// this API; look them up with GET /api/assessors and GET /api/tags.
//
// Notes and assessor names are untrusted text (an LLM's note can repeat
// markup or instructions from the feed it read). They go through safeText on
// the way out, and the browser renders them as text only.

var assessorFields = []string{"name", "description", "color"}

// float64Field returns a number field, or nil when it is absent.
func (b jsonBody) float64Field(field string) (*float64, error) {
	v, ok := b[field]
	if !ok {
		return nil, nil
	}
	var x float64
	if err := json.Unmarshal(v, &x); err != nil || math.IsNaN(x) || math.IsInf(x, 0) {
		return nil, fmt.Errorf("%w: %s must be a number", errBody, field)
	}
	return &x, nil
}

func (a *handlers) listAssessors(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	resp, err := a.client.ListAssessors(ctx, &emptypb.Empty{})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	for _, as := range resp.Assessors {
		sanitizeAssessor(as)
	}
	writeProto(w, resp)
}

func (a *handlers) addAssessor(w http.ResponseWriter, r *http.Request) {
	b, ok := readBody(w, r, assessorFields...)
	if !ok {
		return
	}
	name, err1 := b.str("name")
	desc, err2 := b.str("description")
	color, err3 := b.str("color")
	if err := firstErr(err1, err2, err3); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	as, err := a.client.AddAssessor(ctx, &pb.AddAssessorRequest{
		Name: valueOr(name, ""), Description: valueOr(desc, ""), Color: valueOr(color, ""),
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeAssessor(as)
	writeProtoStatus(w, http.StatusCreated, as)
}

// updateAssessor patches an assessor: only the fields in the body change,
// and "" clears a description or color.
func (a *handlers) updateAssessor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	b, ok := readBody(w, r, assessorFields...)
	if !ok {
		return
	}
	req := &pb.UpdateAssessorRequest{Id: id}
	var errs [3]error
	req.Name, errs[0] = b.str("name")
	req.Description, errs[1] = b.str("description")
	req.Color, errs[2] = b.str("color")
	if err := firstErr(errs[:]...); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	as, err := a.client.UpdateAssessor(ctx, req)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeAssessor(as)
	writeProto(w, as)
}

// removeAssessor deletes an assessor and all of its assessments.
func (a *handlers) removeAssessor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, ok := readBody(w, r); !ok {
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	if _, err := a.client.RemoveAssessor(ctx, &pb.RemoveAssessorRequest{Id: id}); err != nil {
		writeRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// putAssessment stores an assessment for the item in the path, replacing the
// one with the same assessor and tag: {assessor, tag?, score?, note?}. A
// missing tag is the item as a whole. A score of 0 is a score; leave the
// field out for none.
func (a *handlers) putAssessment(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r)
	if !ok {
		return
	}
	b, ok := readBody(w, r, "assessor", "tag", "score", "note")
	if !ok {
		return
	}
	assessor, err1 := b.id("assessor")
	tag, err2 := b.id("tag")
	score, err3 := b.float64Field("score")
	note, err4 := b.str("note")
	if err := firstErr(err1, err2, err3, err4); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if assessor == 0 {
		writeError(w, http.StatusBadRequest, "assessor is required")
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	as, err := a.client.PutAssessment(ctx, &pb.PutAssessmentRequest{
		ItemId: itemID, AssessorId: assessor, TagId: tag, Score: score, Note: valueOr(note, ""),
	})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	sanitizeAssessment(as)
	writeProto(w, as)
}

// removeAssessment deletes the assessment for the item in the path:
// ?assessor=ID[&tag=ID].
func (a *handlers) removeAssessment(w http.ResponseWriter, r *http.Request) {
	itemID, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, ok := readBody(w, r); !ok {
		return
	}
	v := r.URL.Query()
	assessor, err1 := parseID(v, "assessor")
	tag, err2 := parseID(v, "tag")
	if err := firstErr(err1, err2); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if assessor == 0 {
		writeError(w, http.StatusBadRequest, "assessor is required")
		return
	}
	ctx, cancel := a.rpcContext(r)
	defer cancel()
	if _, err := a.client.RemoveAssessment(ctx, &pb.RemoveAssessmentRequest{ItemId: itemID, AssessorId: assessor, TagId: tag}); err != nil {
		writeRPCError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
