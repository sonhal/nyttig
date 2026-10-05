package client

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// nyttig returns the generated client once the connection is up.
func (c *Client) nyttig(ctx context.Context) (pb.NyttigClient, error) {
	if err := c.ensureConn(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.grpc, nil
}

// AddDigestSeries registers a series for an assessor.
func (c *Client) AddDigestSeries(ctx context.Context, req *pb.AddDigestSeriesRequest) (*pb.DigestSeries, error) {
	g, err := c.nyttig(ctx)
	if err != nil {
		return nil, err
	}
	return g.AddDigestSeries(ctx, req)
}

// UpdateDigestSeries renames or redescribes a series; unset fields are
// unchanged.
func (c *Client) UpdateDigestSeries(ctx context.Context, req *pb.UpdateDigestSeriesRequest) (*pb.DigestSeries, error) {
	g, err := c.nyttig(ctx)
	if err != nil {
		return nil, err
	}
	return g.UpdateDigestSeries(ctx, req)
}

// RemoveDigestSeries deletes a series and its digests.
func (c *Client) RemoveDigestSeries(ctx context.Context, id int64) error {
	g, err := c.nyttig(ctx)
	if err != nil {
		return err
	}
	_, err = g.RemoveDigestSeries(ctx, &pb.RemoveDigestSeriesRequest{Id: id})
	return err
}

// ListDigestSeries returns the series of one assessor (0 = all) in display
// order.
func (c *Client) ListDigestSeries(ctx context.Context, assessorID int64) (*pb.ListDigestSeriesResponse, error) {
	g, err := c.nyttig(ctx)
	if err != nil {
		return nil, err
	}
	return g.ListDigestSeries(ctx, &pb.ListDigestSeriesRequest{AssessorId: assessorID})
}

// ReorderDigestSeries sets the display order; ids must list every series once.
func (c *Client) ReorderDigestSeries(ctx context.Context, ids []int64) (*pb.ListDigestSeriesResponse, error) {
	g, err := c.nyttig(ctx)
	if err != nil {
		return nil, err
	}
	return g.ReorderDigestSeries(ctx, &pb.ReorderDigestSeriesRequest{Ids: ids})
}

// AddDigest creates a digest in a series.
func (c *Client) AddDigest(ctx context.Context, req *pb.AddDigestRequest) (*pb.Digest, error) {
	g, err := c.nyttig(ctx)
	if err != nil {
		return nil, err
	}
	return g.AddDigest(ctx, req)
}

// UpdateDigest overwrites the fields and link sets that are set.
func (c *Client) UpdateDigest(ctx context.Context, req *pb.UpdateDigestRequest) (*pb.Digest, error) {
	g, err := c.nyttig(ctx)
	if err != nil {
		return nil, err
	}
	return g.UpdateDigest(ctx, req)
}

// RemoveDigest deletes a digest.
func (c *Client) RemoveDigest(ctx context.Context, id int64) error {
	g, err := c.nyttig(ctx)
	if err != nil {
		return err
	}
	_, err = g.RemoveDigest(ctx, &pb.RemoveDigestRequest{Id: id})
	return err
}

// GetDigest returns one digest with its body, linked items and inputs.
func (c *Client) GetDigest(ctx context.Context, id int64) (*pb.Digest, error) {
	g, err := c.nyttig(ctx)
	if err != nil {
		return nil, err
	}
	return g.GetDigest(ctx, &pb.GetDigestRequest{Id: id})
}

// ListDigests returns a page of a series' digests, newest period first.
func (c *Client) ListDigests(ctx context.Context, req *pb.ListDigestsRequest) (*pb.ListDigestsResponse, error) {
	g, err := c.nyttig(ctx)
	if err != nil {
		return nil, err
	}
	return g.ListDigests(ctx, req)
}

// EnsureDigestSeries returns the ID of the assessor's series with that name
// (any case), creating it when it does not exist. If another client creates
// it at the same moment, that one is used. The description is only used when
// the series is created.
func (c *Client) EnsureDigestSeries(ctx context.Context, assessorID int64, name, description string) (int64, error) {
	find := func() (int64, error) {
		resp, err := c.ListDigestSeries(ctx, assessorID)
		if err != nil {
			return 0, err
		}
		for _, s := range resp.Series {
			if strings.EqualFold(s.Name, name) {
				return s.Id, nil
			}
		}
		return 0, nil
	}
	if id, err := find(); err != nil || id != 0 {
		return id, err
	}
	created, err := c.AddDigestSeries(ctx, &pb.AddDigestSeriesRequest{AssessorId: assessorID, Name: name, Description: description})
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
		return 0, fmt.Errorf("could not find or create the series %q", name)
	}
	return id, nil
}
