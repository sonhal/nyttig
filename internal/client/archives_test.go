package client

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func (s *testServer) ArchiveItem(_ context.Context, req *pb.ArchiveItemRequest) (*pb.Archive, error) {
	if req.ItemId != 1 {
		return nil, status.Error(codes.NotFound, "item not found")
	}
	return &pb.Archive{Id: 5, ItemId: req.ItemId, Status: "pending"}, nil
}

func (s *testServer) ListArchives(_ context.Context, req *pb.ListArchivesRequest) (*pb.ListArchivesResponse, error) {
	return &pb.ListArchivesResponse{Archives: []*pb.Archive{{Id: req.BeforeId - 1, Status: req.Status}}, HasMore: req.Limit == 1}, nil
}

func (s *testServer) GetArchive(_ context.Context, req *pb.GetArchiveRequest) (*pb.Archive, error) {
	return &pb.Archive{Id: req.Id, Status: "done"}, nil
}

func (s *testServer) RemoveArchive(_ context.Context, req *pb.RemoveArchiveRequest) (*emptypb.Empty, error) {
	if req.Id != 5 {
		return nil, status.Error(codes.NotFound, "archive not found")
	}
	return &emptypb.Empty{}, nil
}

func (s *testServer) GetArchiveContent(req *pb.GetArchiveContentRequest, stream pb.Nyttig_GetArchiveContentServer) error {
	if req.Id != 5 {
		return status.Error(codes.NotFound, "archive not found")
	}
	for rest := s.archiveContent; len(rest) > 0; {
		n := min(len(rest), 1<<20)
		if err := stream.Send(&pb.ArchiveContentChunk{Data: rest[:n]}); err != nil {
			return err
		}
		rest = rest[n:]
	}
	return nil
}

func TestArchiveWrappers(t *testing.T) {
	c, _, cleanup := setupTest(t)
	defer cleanup()
	ctx := context.Background()

	a, err := c.ArchiveItem(ctx, 1)
	if err != nil || a.Id != 5 || a.Status != "pending" {
		t.Fatalf("ArchiveItem = %v, %v", a, err)
	}
	if _, err := c.ArchiveItem(ctx, 2); status.Code(err) != codes.NotFound {
		t.Fatalf("ArchiveItem(unknown) err = %v, want NotFound", err)
	}

	list, err := c.ListArchives(ctx, "failed", 10, 1)
	if err != nil || len(list.Archives) != 1 || list.Archives[0].Id != 9 || list.Archives[0].Status != "failed" || !list.HasMore {
		t.Fatalf("ListArchives = %v, %v", list, err)
	}
	if got, err := c.GetArchive(ctx, 5); err != nil || got.Id != 5 {
		t.Fatalf("GetArchive = %v, %v", got, err)
	}
	if err := c.RemoveArchive(ctx, 5); err != nil {
		t.Fatalf("RemoveArchive: %v", err)
	}
	if err := c.RemoveArchive(ctx, 6); status.Code(err) != codes.NotFound {
		t.Fatalf("RemoveArchive(unknown) err = %v, want NotFound", err)
	}
}

func TestGetArchiveContent_WritesEveryChunk(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()
	ts.archiveContent = bytes.Repeat([]byte("0123456789abcdef"), (2<<20+500)/16)

	var buf bytes.Buffer
	n, err := c.GetArchiveContent(context.Background(), 5, &buf)
	if err != nil || n != int64(len(ts.archiveContent)) || !bytes.Equal(buf.Bytes(), ts.archiveContent) {
		t.Fatalf("GetArchiveContent = %d, %v; wrote %d bytes, want %d", n, err, buf.Len(), len(ts.archiveContent))
	}
	if _, err := c.GetArchiveContent(context.Background(), 6, &buf); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown archive err = %v, want NotFound", err)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestGetArchiveContent_StopsOnWriteError(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()
	ts.archiveContent = []byte("page")

	boom := errors.New("disk full")
	if _, err := c.GetArchiveContent(context.Background(), 5, failingWriter{boom}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the writer's error", err)
	}
}
