package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/archives"
	"github.com/sonhal/nyttig/internal/server/db"
)

// archiveChunkSize is the most uncompressed bytes in one ArchiveContentChunk;
// gRPC's default message limit is 4 MiB.
const archiveChunkSize = 1 << 20

// SetArchives enables the archive RPCs. dir holds the archive files; wake is
// called after an item is queued, to start a worker. Without it, the RPCs
// return Unavailable.
func (s *Service) SetArchives(dir string, wake func()) {
	s.archiveDir = dir
	s.archiveWake = wake
}

func (s *Service) archivesEnabled() error {
	if s.archiveDir == "" {
		return status.Error(codes.Unavailable, "archiving is not configured")
	}
	return nil
}

// PushItemUpdate sends the item as it is now to the stream subscribers, so
// clients see a changed archive status. The write has already succeeded, so a
// failure is only logged.
func (s *Service) PushItemUpdate(itemID int64) { s.pushItemUpdate(itemID) }

// ArchiveItem queues the article behind an item for archiving. An item that
// is already archived is queued again (its old copy stays until the new one
// succeeds); one that is pending or running is returned as it is. An unknown
// item is NotFound, a link that is not an absolute http(s) URL is
// InvalidArgument.
func (s *Service) ArchiveItem(ctx context.Context, req *pb.ArchiveItemRequest) (*pb.Archive, error) {
	if err := s.archivesEnabled(); err != nil {
		return nil, err
	}
	if req.ItemId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "item_id is required")
	}
	item, err := db.GetItem(s.db, req.ItemId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get item: %v", err)
	}
	if item == nil {
		return nil, status.Errorf(codes.NotFound, "item %d not found", req.ItemId)
	}
	if err := validateArchiveLink(item.Link); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	a, err := db.EnqueueArchive(s.db, req.ItemId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "queue archive: %v", err)
	}
	if a == nil { // the item was deleted since GetItem
		return nil, status.Errorf(codes.NotFound, "item %d not found", req.ItemId)
	}
	if s.archiveWake != nil {
		s.archiveWake()
	}
	s.pushItemUpdate(req.ItemId)
	return dbArchiveToProto(a), nil
}

// ListArchives returns archives newest first, optionally of one status.
func (s *Service) ListArchives(ctx context.Context, req *pb.ListArchivesRequest) (*pb.ListArchivesResponse, error) {
	if err := firstErr(validateArchiveStatus(req.Status), validateArchivePage(req.BeforeId, req.Limit)); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	list, more, err := db.ListArchives(s.db, req.Status, req.BeforeId, int(req.Limit))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list archives: %v", err)
	}
	resp := &pb.ListArchivesResponse{Archives: make([]*pb.Archive, len(list)), HasMore: more}
	for i, a := range list {
		resp.Archives[i] = dbArchiveToProto(a)
	}
	return resp, nil
}

// GetArchive returns one archive's metadata. An unknown ID is NotFound.
func (s *Service) GetArchive(ctx context.Context, req *pb.GetArchiveRequest) (*pb.Archive, error) {
	a, err := s.findArchive(req.Id)
	if err != nil {
		return nil, err
	}
	return dbArchiveToProto(a), nil
}

func (s *Service) findArchive(id int64) (*db.Archive, error) {
	if id <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	a, err := db.GetArchive(s.db, id)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get archive: %v", err)
	}
	if a == nil {
		return nil, status.Errorf(codes.NotFound, "archive %d not found", id)
	}
	return a, nil
}

// GetArchiveContent streams the saved page in chunks of up to 1 MiB, whatever
// the archive's status: a failed re-archive still serves its older copy. An
// archive without a copy (size 0) is FailedPrecondition.
func (s *Service) GetArchiveContent(req *pb.GetArchiveContentRequest, stream pb.Nyttig_GetArchiveContentServer) error {
	if err := s.archivesEnabled(); err != nil {
		return err
	}
	a, err := s.findArchive(req.Id)
	if err != nil {
		return err
	}
	if a.Size <= 0 {
		return status.Errorf(codes.FailedPrecondition, "archive %d is %s and has no copy", a.ID, a.Status)
	}
	r, err := archives.OpenFile(s.archiveDir, a.ID, a.ContentType)
	if errors.Is(err, os.ErrNotExist) {
		return status.Errorf(codes.Internal, "the file of archive %d is missing", a.ID)
	}
	if err != nil {
		return status.Errorf(codes.Internal, "open archive: %v", err)
	}
	defer func() { _ = r.Close() }()

	buf := make([]byte, archiveChunkSize)
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			if serr := stream.Send(&pb.ArchiveContentChunk{Data: buf[:n]}); serr != nil {
				return serr
			}
		}
		if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		if err != nil {
			return status.Errorf(codes.Internal, "read archive: %v", err)
		}
	}
}

// RemoveArchive deletes an archive and its file. An unknown ID is NotFound.
func (s *Service) RemoveArchive(ctx context.Context, req *pb.RemoveArchiveRequest) (*emptypb.Empty, error) {
	if err := s.archivesEnabled(); err != nil {
		return nil, err
	}
	a, err := s.findArchive(req.Id)
	if err != nil {
		return nil, err
	}
	deleted, err := db.DeleteArchive(s.db, a.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "delete archive: %v", err)
	}
	if !deleted {
		return nil, status.Errorf(codes.NotFound, "archive %d not found", a.ID)
	}
	if err := archives.RemoveFiles(s.archiveDir, a.ID); err != nil {
		// The row is gone; the next start's reconcile deletes the file.
		slog.Warn("remove archive file", "archive_id", a.ID, "error", err)
	}
	if a.ItemID != 0 {
		s.pushItemUpdate(a.ItemID)
	}
	return &emptypb.Empty{}, nil
}

func dbArchiveToProto(a *db.Archive) *pb.Archive {
	p := &pb.Archive{
		Id:            a.ID,
		ItemId:        a.ItemID,
		Link:          a.Link,
		Title:         a.Title,
		SourceName:    a.SourceName,
		Status:        a.Status,
		Error:         a.Error,
		Note:          a.Note,
		FinalUrl:      a.FinalURL,
		ContentType:   a.ContentType,
		HttpStatus:    int32(a.HTTPStatus),
		Size:          a.Size,
		OriginalSize:  a.OriginalSize,
		MissingAssets: int32(a.MissingAssets),
		RequestedAt:   timestamppb.New(a.RequestedAt),
	}
	if a.StartedAt != nil {
		p.StartedAt = timestamppb.New(*a.StartedAt)
	}
	if a.FinishedAt != nil {
		p.FinishedAt = timestamppb.New(*a.FinishedAt)
	}
	return p
}
