package service

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/db"
)

// ── Assessors ──────────────────────────────────────────────────────────────

// AddAssessor registers an assessor. A duplicate name is AlreadyExists.
func (s *Service) AddAssessor(ctx context.Context, req *pb.AddAssessorRequest) (*pb.Assessor, error) {
	if err := firstErr(
		validateName("name", req.Name, maxAssessorNameLen),
		validateColor(req.Color),
		validateAssessorDescription(req.Description),
	); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	id, err := db.InsertAssessor(s.db, &db.Assessor{Name: req.Name, Description: req.Description, Color: req.Color})
	if isUniqueViolation(err) {
		return nil, status.Errorf(codes.AlreadyExists, "assessor %q already exists", req.Name)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "insert assessor: %v", err)
	}
	created, err := db.GetAssessor(s.db, id)
	if err != nil || created == nil {
		return nil, status.Errorf(codes.Internal, "get created assessor: %v", err)
	}
	return dbAssessorToProto(created), nil
}

// UpdateAssessor renames, recolors and/or redescribes an assessor. Unset
// fields are unchanged.
func (s *Service) UpdateAssessor(ctx context.Context, req *pb.UpdateAssessorRequest) (*pb.Assessor, error) {
	a, err := db.GetAssessor(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get assessor: %v", err)
	}
	if a == nil {
		return nil, status.Errorf(codes.NotFound, "assessor %d not found", req.Id)
	}

	var errs []error
	if req.Name != nil {
		errs = append(errs, validateName("name", *req.Name, maxAssessorNameLen))
		a.Name = *req.Name
	}
	if req.Description != nil {
		errs = append(errs, validateAssessorDescription(*req.Description))
		a.Description = *req.Description
	}
	if req.Color != nil {
		errs = append(errs, validateColor(*req.Color))
		a.Color = *req.Color
	}
	if err := firstErr(errs...); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	_, err = db.UpdateAssessor(s.db, a)
	if isUniqueViolation(err) {
		return nil, status.Errorf(codes.AlreadyExists, "assessor %q already exists", a.Name)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "update assessor: %v", err)
	}
	updated, err := db.GetAssessor(s.db, req.Id)
	if err != nil || updated == nil {
		return nil, status.Errorf(codes.Internal, "get updated assessor: %v", err)
	}
	return dbAssessorToProto(updated), nil
}

// RemoveAssessor deletes an assessor and all of its assessments. Saved views
// that use it keep existing without the assessor fields. An unknown ID is
// NotFound.
func (s *Service) RemoveAssessor(ctx context.Context, req *pb.RemoveAssessorRequest) (*emptypb.Empty, error) {
	deleted, err := db.DeleteAssessor(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete assessor: %v", err)
	}
	if !deleted {
		return nil, status.Errorf(codes.NotFound, "assessor %d not found", req.Id)
	}
	return &emptypb.Empty{}, nil
}

// ListAssessors returns every assessor ordered by name.
func (s *Service) ListAssessors(ctx context.Context, _ *emptypb.Empty) (*pb.ListAssessorsResponse, error) {
	list, err := db.ListAssessors(s.db)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list assessors: %v", err)
	}
	resp := &pb.ListAssessorsResponse{Assessors: make([]*pb.Assessor, len(list))}
	for i, a := range list {
		resp.Assessors[i] = dbAssessorToProto(a)
	}
	return resp, nil
}

// checkAssessor reports NotFound for an assessor that does not exist.
func (s *Service) checkAssessor(id int64) error {
	a, err := db.GetAssessor(s.db, id)
	if err != nil {
		return status.Errorf(codes.Internal, "get assessor: %v", err)
	}
	if a == nil {
		return status.Errorf(codes.NotFound, "assessor %d not found", id)
	}
	return nil
}

// ── Assessments ────────────────────────────────────────────────────────────

// validateAssessmentKey checks the ids that name an assessment.
func validateAssessmentKey(itemID, assessorID, tagID int64) error {
	if itemID <= 0 {
		return errors.New("item_id is required")
	}
	if assessorID <= 0 {
		return errors.New("assessor_id is required")
	}
	if tagID < 0 {
		return errors.New("tag_id must not be negative")
	}
	return nil
}

// PutAssessment stores an assessment, replacing the one with the same
// (item, assessor, tag). An unknown item, assessor or tag is NotFound.
func (s *Service) PutAssessment(ctx context.Context, req *pb.PutAssessmentRequest) (*pb.Assessment, error) {
	errs := []error{
		validateAssessmentKey(req.ItemId, req.AssessorId, req.TagId),
		validateNote(req.Note),
	}
	if req.Score != nil {
		errs = append(errs, validateScore("score", *req.Score))
	} else if req.Note == "" {
		errs = append(errs, errors.New("a score or a note is required"))
	}
	if err := firstErr(errs...); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	item, err := db.GetItem(s.db, req.ItemId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get item: %v", err)
	}
	if item == nil {
		return nil, status.Errorf(codes.NotFound, "item %d not found", req.ItemId)
	}
	if err := s.checkAssessor(req.AssessorId); err != nil {
		return nil, err
	}
	if req.TagId != 0 {
		tag, err := db.GetTag(s.db, req.TagId)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "get tag: %v", err)
		}
		if tag == nil {
			return nil, status.Errorf(codes.NotFound, "tag %d not found", req.TagId)
		}
	}

	stored, err := db.PutAssessment(s.db, &db.Assessment{
		ItemID: req.ItemId, AssessorID: req.AssessorId, TagID: req.TagId,
		Score: req.Score, Note: req.Note,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "put assessment: %v", err)
	}
	s.pushItemUpdate(req.ItemId)
	return dbAssessmentToProto(stored), nil
}

// pushItemUpdate sends the item, with all its assessments, to the stream
// subscribers. The write has already succeeded, so a failure is only logged.
func (s *Service) pushItemUpdate(itemID int64) {
	item, err := db.GetItem(s.db, itemID)
	if err != nil || item == nil {
		slog.Warn("push assessment update", "item_id", itemID, "error", err)
		return
	}
	s.hub.PushUpdate(ItemToProto(item))
}

// RemoveAssessment deletes the assessment with that key. An unknown one is
// NotFound.
func (s *Service) RemoveAssessment(ctx context.Context, req *pb.RemoveAssessmentRequest) (*emptypb.Empty, error) {
	if err := validateAssessmentKey(req.ItemId, req.AssessorId, req.TagId); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	deleted, err := db.DeleteAssessment(s.db, req.ItemId, req.AssessorId, req.TagId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete assessment: %v", err)
	}
	if !deleted {
		return nil, status.Errorf(codes.NotFound, "assessment not found")
	}
	s.pushItemUpdate(req.ItemId)
	return &emptypb.Empty{}, nil
}

// ── Conversion helpers ─────────────────────────────────────────────────────

func dbAssessorToProto(a *db.Assessor) *pb.Assessor {
	return &pb.Assessor{
		Id:          a.ID,
		Name:        a.Name,
		Description: a.Description,
		Color:       a.Color,
		CreatedAt:   timestamppb.New(a.CreatedAt),
	}
}

func dbAssessmentToProto(a *db.Assessment) *pb.Assessment {
	p := &pb.Assessment{
		Id:           a.ID,
		ItemId:       a.ItemID,
		AssessorId:   a.AssessorID,
		AssessorName: a.AssessorName,
		TagId:        a.TagID,
		Note:         a.Note,
		UpdatedAt:    timestamppb.New(a.UpdatedAt),
	}
	if a.Score != nil {
		score := *a.Score
		p.Score = &score
	}
	return p
}
