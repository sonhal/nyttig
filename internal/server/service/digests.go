package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/db"
)

// ── Digest series ──────────────────────────────────────────────────────────

// AddDigestSeries registers a series for an assessor. An unknown assessor is
// NotFound; a name the assessor already has, in any case, is AlreadyExists.
func (s *Service) AddDigestSeries(ctx context.Context, req *pb.AddDigestSeriesRequest) (*pb.DigestSeries, error) {
	if req.AssessorId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "assessor_id is required")
	}
	if err := firstErr(
		validateName("name", req.Name, maxSeriesNameLen),
		validateAssessorDescription(req.Description),
	); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.checkAssessor(req.AssessorId); err != nil {
		return nil, err
	}
	id, err := db.InsertDigestSeries(s.db, &db.DigestSeries{AssessorID: req.AssessorId, Name: req.Name, Description: req.Description})
	if isUniqueViolation(err) {
		return nil, status.Errorf(codes.AlreadyExists, "series %q already exists for assessor %d", req.Name, req.AssessorId)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "insert digest series: %v", err)
	}
	return s.digestSeriesProto(id)
}

func (s *Service) digestSeriesProto(id int64) (*pb.DigestSeries, error) {
	got, err := db.GetDigestSeries(s.db, id)
	if err != nil || got == nil {
		return nil, status.Errorf(codes.Internal, "get digest series: %v", err)
	}
	return dbDigestSeriesToProto(got), nil
}

// UpdateDigestSeries renames or redescribes a series; unset fields are
// unchanged. A series is not moved between assessors.
func (s *Service) UpdateDigestSeries(ctx context.Context, req *pb.UpdateDigestSeriesRequest) (*pb.DigestSeries, error) {
	cur, err := db.GetDigestSeries(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get digest series: %v", err)
	}
	if cur == nil {
		return nil, status.Errorf(codes.NotFound, "digest series %d not found", req.Id)
	}
	var errs []error
	if req.Name != nil {
		errs = append(errs, validateName("name", *req.Name, maxSeriesNameLen))
		cur.Name = *req.Name
	}
	if req.Description != nil {
		errs = append(errs, validateAssessorDescription(*req.Description))
		cur.Description = *req.Description
	}
	if err := firstErr(errs...); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	_, err = db.UpdateDigestSeries(s.db, cur)
	if isUniqueViolation(err) {
		return nil, status.Errorf(codes.AlreadyExists, "series %q already exists for assessor %d", cur.Name, cur.AssessorID)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "update digest series: %v", err)
	}
	return s.digestSeriesProto(req.Id)
}

// RemoveDigestSeries deletes a series and its digests. An unknown ID is
// NotFound.
func (s *Service) RemoveDigestSeries(ctx context.Context, req *pb.RemoveDigestSeriesRequest) (*emptypb.Empty, error) {
	deleted, err := db.DeleteDigestSeries(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete digest series: %v", err)
	}
	if !deleted {
		return nil, status.Errorf(codes.NotFound, "digest series %d not found", req.Id)
	}
	return &emptypb.Empty{}, nil
}

// ListDigestSeries returns the series of one assessor (assessor_id 0 = all),
// in display order.
func (s *Service) ListDigestSeries(ctx context.Context, req *pb.ListDigestSeriesRequest) (*pb.ListDigestSeriesResponse, error) {
	if req.AssessorId < 0 {
		return nil, status.Error(codes.InvalidArgument, "assessor_id must not be negative")
	}
	return s.digestSeriesResponse(req.AssessorId)
}

// ReorderDigestSeries sets the display order. ids must list every series
// exactly once.
func (s *Service) ReorderDigestSeries(ctx context.Context, req *pb.ReorderDigestSeriesRequest) (*pb.ListDigestSeriesResponse, error) {
	err := db.ReorderDigestSeries(s.db, req.Ids)
	if errors.Is(err, db.ErrDigestSeriesOrder) {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "reorder digest series: %v", err)
	}
	return s.digestSeriesResponse(0)
}

func (s *Service) digestSeriesResponse(assessorID int64) (*pb.ListDigestSeriesResponse, error) {
	list, err := db.ListDigestSeries(s.db, assessorID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list digest series: %v", err)
	}
	resp := &pb.ListDigestSeriesResponse{Series: make([]*pb.DigestSeries, len(list))}
	for i, sr := range list {
		resp.Series[i] = dbDigestSeriesToProto(sr)
	}
	return resp, nil
}

// ── Digests ────────────────────────────────────────────────────────────────

// utcSecond normalizes a client timestamp to UTC with whole seconds.
func utcSecond(ts *timestamppb.Timestamp) time.Time { return ts.AsTime().UTC().Truncate(time.Second) }

func validatePeriodTimestamp(field string, ts *timestamppb.Timestamp) error {
	if ts == nil {
		return fmt.Errorf("%s is required", field)
	}
	if err := ts.CheckValid(); err != nil {
		return fmt.Errorf("%s: %v", field, err)
	}
	return nil
}

// checkDigestLinks reports NotFound, naming the first ID, for an item or an
// input digest that does not exist.
func (s *Service) checkDigestLinks(itemIDs, inputIDs []int64) error {
	if id, err := db.FirstMissingItemID(s.db, itemIDs); err != nil {
		return status.Errorf(codes.Internal, "check items: %v", err)
	} else if id != 0 {
		return status.Errorf(codes.NotFound, "item %d not found", id)
	}
	if id, err := db.FirstMissingDigestID(s.db, inputIDs); err != nil {
		return status.Errorf(codes.Internal, "check input digests: %v", err)
	} else if id != 0 {
		return status.Errorf(codes.NotFound, "digest %d not found", id)
	}
	return nil
}

// AddDigest always creates a new digest in a series. An unknown series, item
// or input digest is NotFound.
func (s *Service) AddDigest(ctx context.Context, req *pb.AddDigestRequest) (*pb.Digest, error) {
	if req.SeriesId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "series_id is required")
	}
	items, itemsErr := validateDigestIDs("item_ids", req.ItemIds, maxDigestItems)
	inputs, inputsErr := validateDigestIDs("input_ids", req.InputIds, maxDigestInputs)
	if err := firstErr(
		validateName("title", req.Title, maxDigestTitleLen),
		validateDigestBody(req.Body),
		validatePeriodTimestamp("period_start", req.PeriodStart),
		validatePeriodTimestamp("period_end", req.PeriodEnd),
		itemsErr, inputsErr,
	); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	start, end := utcSecond(req.PeriodStart), utcSecond(req.PeriodEnd)
	if end.Before(start) {
		return nil, status.Error(codes.InvalidArgument, "period_end must not be before period_start")
	}

	series, err := db.GetDigestSeries(s.db, req.SeriesId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get digest series: %v", err)
	}
	if series == nil {
		return nil, status.Errorf(codes.NotFound, "digest series %d not found", req.SeriesId)
	}
	if err := s.checkDigestLinks(items, inputs); err != nil {
		return nil, err
	}

	id, err := db.InsertDigest(s.db, &db.NewDigest{
		SeriesID: req.SeriesId, Title: req.Title, Body: req.Body,
		PeriodStart: start, PeriodEnd: end, ItemIDs: items, InputIDs: inputs,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "insert digest: %v", err)
	}
	return s.digestProto(id)
}

func (s *Service) digestProto(id int64) (*pb.Digest, error) {
	d, err := db.GetDigest(s.db, id)
	if err != nil || d == nil {
		return nil, status.Errorf(codes.Internal, "get digest: %v", err)
	}
	return dbDigestToProto(d), nil
}

// UpdateDigest overwrites the fields and link sets that are set; there are no
// revisions. An unknown digest, item or input is NotFound.
func (s *Service) UpdateDigest(ctx context.Context, req *pb.UpdateDigestRequest) (*pb.Digest, error) {
	cur, err := db.GetDigest(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get digest: %v", err)
	}
	if cur == nil {
		return nil, status.Errorf(codes.NotFound, "digest %d not found", req.Id)
	}

	u := &db.DigestUpdate{}
	var errs []error
	if req.Title != nil {
		errs = append(errs, validateName("title", *req.Title, maxDigestTitleLen))
		u.Title = req.Title
	}
	if req.Body != nil {
		errs = append(errs, validateDigestBody(*req.Body))
		u.Body = req.Body
	}
	start, end := cur.PeriodStart, cur.PeriodEnd
	if req.PeriodStart != nil {
		if err := validatePeriodTimestamp("period_start", req.PeriodStart); err != nil {
			errs = append(errs, err)
		} else {
			start = utcSecond(req.PeriodStart)
			u.PeriodStart = &start
		}
	}
	if req.PeriodEnd != nil {
		if err := validatePeriodTimestamp("period_end", req.PeriodEnd); err != nil {
			errs = append(errs, err)
		} else {
			end = utcSecond(req.PeriodEnd)
			u.PeriodEnd = &end
		}
	}
	var items, inputs []int64
	if req.ItemIds != nil {
		var err error
		items, err = validateDigestIDs("item_ids", req.ItemIds.Ids, maxDigestItems)
		errs = append(errs, err)
		u.ItemIDs = &items
	}
	if req.InputIds != nil {
		var err error
		inputs, err = validateDigestIDs("input_ids", req.InputIds.Ids, maxDigestInputs)
		errs = append(errs, err)
		u.InputIDs = &inputs
		for _, id := range inputs {
			if id == req.Id {
				errs = append(errs, errors.New("a digest cannot be its own input"))
			}
		}
	}
	if err := firstErr(errs...); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Also when only one end is set: the stored one may now be on the wrong
	// side of it.
	if end.Before(start) {
		return nil, status.Error(codes.InvalidArgument, "period_end must not be before period_start")
	}
	if err := s.checkDigestLinks(items, inputs); err != nil {
		return nil, err
	}

	if _, err := db.UpdateDigest(s.db, req.Id, u); err != nil {
		return nil, status.Errorf(codes.Internal, "update digest: %v", err)
	}
	return s.digestProto(req.Id)
}

// RemoveDigest deletes a digest and removes it from other digests' inputs. An
// unknown ID is NotFound.
func (s *Service) RemoveDigest(ctx context.Context, req *pb.RemoveDigestRequest) (*emptypb.Empty, error) {
	deleted, err := db.DeleteDigest(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete digest: %v", err)
	}
	if !deleted {
		return nil, status.Errorf(codes.NotFound, "digest %d not found", req.Id)
	}
	return &emptypb.Empty{}, nil
}

// GetDigest returns one digest with its body, linked items and inputs.
func (s *Service) GetDigest(ctx context.Context, req *pb.GetDigestRequest) (*pb.Digest, error) {
	d, err := db.GetDigest(s.db, req.Id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get digest: %v", err)
	}
	if d == nil {
		return nil, status.Errorf(codes.NotFound, "digest %d not found", req.Id)
	}
	return dbDigestToProto(d), nil
}

// ListDigests returns a series' digests, newest period first, paged by the
// before_id cursor. Bodies are left out unless include_body.
func (s *Service) ListDigests(ctx context.Context, req *pb.ListDigestsRequest) (*pb.ListDigestsResponse, error) {
	switch {
	case req.SeriesId <= 0:
		return nil, status.Error(codes.InvalidArgument, "series_id is required")
	case req.BeforeId < 0:
		return nil, status.Error(codes.InvalidArgument, "before_id must not be negative")
	case req.Limit < 0:
		return nil, status.Error(codes.InvalidArgument, "limit must not be negative")
	}
	series, err := db.GetDigestSeries(s.db, req.SeriesId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get digest series: %v", err)
	}
	if series == nil {
		return nil, status.Errorf(codes.NotFound, "digest series %d not found", req.SeriesId)
	}
	list, more, err := db.ListDigests(s.db, req.SeriesId, req.BeforeId, int(req.Limit), req.IncludeBody)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list digests: %v", err)
	}
	resp := &pb.ListDigestsResponse{Digests: make([]*pb.Digest, len(list)), HasMore: more}
	for i, d := range list {
		resp.Digests[i] = dbDigestToProto(d)
	}
	return resp, nil
}

// ── Conversion helpers ─────────────────────────────────────────────────────

func dbDigestSeriesToProto(s *db.DigestSeries) *pb.DigestSeries {
	p := &pb.DigestSeries{
		Id:           s.ID,
		AssessorId:   s.AssessorID,
		AssessorName: s.AssessorName,
		Name:         s.Name,
		Description:  s.Description,
		Position:     int32(s.Position),
		CreatedAt:    timestamppb.New(s.CreatedAt),
		DigestCount:  int32(s.DigestCount),
	}
	if s.LatestPeriodEnd != nil {
		p.LatestPeriodEnd = timestamppb.New(*s.LatestPeriodEnd)
	}
	return p
}

func dbDigestToProto(d *db.Digest) *pb.Digest {
	p := &pb.Digest{
		Id:           d.ID,
		SeriesId:     d.SeriesID,
		SeriesName:   d.SeriesName,
		AssessorId:   d.AssessorID,
		AssessorName: d.AssessorName,
		Title:        d.Title,
		Body:         d.Body,
		PeriodStart:  timestamppb.New(d.PeriodStart),
		PeriodEnd:    timestamppb.New(d.PeriodEnd),
		CreatedAt:    timestamppb.New(d.CreatedAt),
		UpdatedAt:    timestamppb.New(d.UpdatedAt),
	}
	for _, it := range d.Items {
		di := &pb.DigestItem{ItemId: it.ItemID, Title: it.Title, Link: it.Link, SourceName: it.SourceName}
		if it.Published != nil {
			di.Published = timestamppb.New(*it.Published)
		}
		p.Items = append(p.Items, di)
	}
	for _, r := range d.Inputs {
		p.Inputs = append(p.Inputs, &pb.DigestRef{Id: r.ID, Title: r.Title, SeriesName: r.SeriesName, PeriodEnd: timestamppb.New(r.PeriodEnd)})
	}
	return p
}
