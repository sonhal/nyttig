package client

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// The built-in assessor for rating items yourself. It is created the first
// time a client needs it (EnsureMe); your scores are a ground truth to
// compare the other assessors against.
const (
	MeAssessorName        = "me"
	MeAssessorDescription = "Your own ratings, 0 to 1"
	MeAssessorColor       = "#4EC9B0"
)

// EnsureMe returns the ID of the assessor "me", creating it when it does not
// exist yet. If another client creates it at the same moment, that one is
// used.
func (c *Client) EnsureMe(ctx context.Context) (int64, error) {
	find := func() (int64, error) {
		resp, err := c.ListAssessors(ctx)
		if err != nil {
			return 0, err
		}
		for _, a := range resp.Assessors {
			if a.Name == MeAssessorName {
				return a.Id, nil
			}
		}
		return 0, nil
	}
	if id, err := find(); err != nil || id != 0 {
		return id, err
	}
	created, err := c.AddAssessor(ctx, &pb.AddAssessorRequest{
		Name: MeAssessorName, Description: MeAssessorDescription, Color: MeAssessorColor,
	})
	if err == nil {
		return created.Id, nil
	}
	if status.Code(err) != codes.AlreadyExists {
		return 0, err
	}
	id, ferr := find()
	if ferr != nil {
		return 0, ferr
	}
	if id == 0 {
		return 0, errors.New(`could not find or create the assessor "me"`)
	}
	return id, nil
}

// Rate scores an item as the assessor "me" (created on first use), for the
// item as a whole. The score must be from 0 to 1; the daemon checks that. A
// note is optional.
func (c *Client) Rate(ctx context.Context, itemID int64, score float64, note string) (*pb.Assessment, error) {
	id, err := c.EnsureMe(ctx)
	if err != nil {
		return nil, fmt.Errorf("assessor %q: %w", MeAssessorName, err)
	}
	return c.PutAssessment(ctx, &pb.PutAssessmentRequest{ItemId: itemID, AssessorId: id, Score: &score, Note: note})
}
