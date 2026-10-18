package client

import (
	"context"
	"errors"
	"io"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// ArchiveItem queues the article behind an item for archiving and returns
// the archive (pending, or as it is when it is already queued).
func (c *Client) ArchiveItem(ctx context.Context, itemID int64) (*pb.Archive, error) {
	g, err := c.nyttig(ctx)
	if err != nil {
		return nil, err
	}
	return g.ArchiveItem(ctx, &pb.ArchiveItemRequest{ItemId: itemID})
}

// ListArchives returns archives newest first. status "" lists every status;
// beforeID continues after a page (0 = from the newest); limit 0 is the
// daemon's default.
func (c *Client) ListArchives(ctx context.Context, status string, beforeID int64, limit int32) (*pb.ListArchivesResponse, error) {
	g, err := c.nyttig(ctx)
	if err != nil {
		return nil, err
	}
	return g.ListArchives(ctx, &pb.ListArchivesRequest{Status: status, BeforeId: beforeID, Limit: limit})
}

// GetArchive returns one archive's metadata.
func (c *Client) GetArchive(ctx context.Context, id int64) (*pb.Archive, error) {
	g, err := c.nyttig(ctx)
	if err != nil {
		return nil, err
	}
	return g.GetArchive(ctx, &pb.GetArchiveRequest{Id: id})
}

// RemoveArchive deletes an archive and its file.
func (c *Client) RemoveArchive(ctx context.Context, id int64) error {
	g, err := c.nyttig(ctx)
	if err != nil {
		return err
	}
	_, err = g.RemoveArchive(ctx, &pb.RemoveArchiveRequest{Id: id})
	return err
}

// GetArchiveContent writes the saved page to w and returns the number of
// bytes written. The archive must be done or partial. On an error part of the
// page may already have been written.
func (c *Client) GetArchiveContent(ctx context.Context, id int64, w io.Writer) (int64, error) {
	g, err := c.nyttig(ctx)
	if err != nil {
		return 0, err
	}
	stream, err := g.GetArchiveContent(ctx, &pb.GetArchiveContentRequest{Id: id})
	if err != nil {
		return 0, err
	}
	var total int64
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
		n, err := w.Write(chunk.Data)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}
}
