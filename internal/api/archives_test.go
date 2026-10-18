package api

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// archiveFake is the archive state of a test; fakeClient's archive RPCs read it.
type archiveFake struct {
	archives []*pb.Archive
	chunks   [][]byte
	// contentErr fails GetArchiveContent's first Recv.
	contentErr error
	lastList   *pb.ListArchivesRequest
}

func (f *fakeClient) arch() *archiveFake { return &f.archive }

func (f *fakeClient) ArchiveItem(_ context.Context, req *pb.ArchiveItemRequest, _ ...grpc.CallOption) (*pb.Archive, error) {
	f.record(req)
	if f.err != nil {
		return nil, f.err
	}
	if as := f.arch().archives; len(as) > 0 {
		return proto.Clone(as[0]).(*pb.Archive), nil
	}
	return &pb.Archive{Id: 3, ItemId: req.ItemId, Status: "pending"}, nil
}

func (f *fakeClient) ListArchives(_ context.Context, req *pb.ListArchivesRequest, _ ...grpc.CallOption) (*pb.ListArchivesResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	a := f.arch()
	a.lastList = req
	out := &pb.ListArchivesResponse{HasMore: true}
	for _, ar := range a.archives {
		out.Archives = append(out.Archives, proto.Clone(ar).(*pb.Archive))
	}
	return out, nil
}

func (f *fakeClient) GetArchive(_ context.Context, req *pb.GetArchiveRequest, _ ...grpc.CallOption) (*pb.Archive, error) {
	if f.err != nil {
		return nil, f.err
	}
	for _, ar := range f.arch().archives {
		if ar.Id == req.Id {
			return proto.Clone(ar).(*pb.Archive), nil
		}
	}
	return nil, status.Error(codes.NotFound, "no such archive")
}

func (f *fakeClient) RemoveArchive(_ context.Context, req *pb.RemoveArchiveRequest, _ ...grpc.CallOption) (*emptypb.Empty, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.recordRemove("archive", req.Id)
	return &emptypb.Empty{}, nil
}

type chunkStream struct {
	grpc.ClientStream
	chunks [][]byte
	err    error
}

func (s *chunkStream) Recv() (*pb.ArchiveContentChunk, error) {
	if s.err != nil {
		return nil, s.err
	}
	if len(s.chunks) == 0 {
		return nil, io.EOF
	}
	c := s.chunks[0]
	s.chunks = s.chunks[1:]
	return &pb.ArchiveContentChunk{Data: c}, nil
}

func (f *fakeClient) GetArchiveContent(_ context.Context, _ *pb.GetArchiveContentRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[pb.ArchiveContentChunk], error) {
	a := f.arch()
	return &chunkStream{chunks: a.chunks, err: a.contentErr}, nil
}

func TestArchiveItem(t *testing.T) {
	fc := &fakeClient{}
	th := newTestHandler(t, fc)
	rec := th.do("POST", "/api/items/12/archive", `{}`, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("archive: %d %s", rec.Code, rec.Body)
	}
	if !proto.Equal(fc.lastReq, &pb.ArchiveItemRequest{ItemId: 12}) {
		t.Errorf("request = %v", fc.lastReq)
	}
	if b := rec.Body.String(); !strings.Contains(b, `"status":"pending"`) || !strings.Contains(b, `"id":"3"`) {
		t.Errorf("body: %s", b)
	}
	if rec := th.do("POST", "/api/items/x/archive", `{}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", rec.Code)
	}
	if rec := th.do("POST", "/api/items/12/archive", `{"x":1}`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", rec.Code)
	}
	fc.err = status.Error(codes.NotFound, "no such item")
	if rec := th.do("POST", "/api/items/12/archive", `{}`, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown item: %d", rec.Code)
	}
}

func TestListArchives(t *testing.T) {
	fc := &fakeClient{}
	fc.arch().archives = []*pb.Archive{{
		Id: 1, ItemId: 5, Status: "failed", Link: "javascript:alert(1)", FinalUrl: "https://example.com/a",
		Title: "evil\x00‮<b>", Error: "bad\x07", Note: "n‮x", SourceName: "s\x00",
	}}
	th := newTestHandler(t, fc)

	rec := th.do("GET", "/api/archives?status=failed&before=9&limit=20", "", nil)
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	want := &pb.ListArchivesRequest{Status: "failed", BeforeId: 9, Limit: 20}
	if !proto.Equal(fc.arch().lastList, want) {
		t.Errorf("request = %v, want %v", fc.arch().lastList, want)
	}
	body := rec.Body.String()
	for _, bad := range []string{"javascript:", `\u0000`, "‮", `\u0007`} {
		if strings.Contains(body, bad) {
			t.Errorf("body not sanitized (%q): %s", bad, body)
		}
	}
	if !strings.Contains(body, `"has_more":true`) || !strings.Contains(body, "https://example.com/a") {
		t.Errorf("body: %s", body)
	}
	for _, q := range []string{"status=bogus", "limit=0", "limit=201", "before=-1"} {
		if rec := th.do("GET", "/api/archives?"+q, "", nil); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, rec.Code)
		}
	}
}

func TestGetArchive(t *testing.T) {
	fc := &fakeClient{}
	fc.arch().archives = []*pb.Archive{{Id: 4, Status: "failed", Error: "HTTP 403\u202e", Link: "data:text/html,x"}}
	th := newTestHandler(t, fc)
	rec := th.do("GET", "/api/archives/4", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"error":"HTTP 403"`) || strings.Contains(rec.Body.String(), "data:") {
		t.Errorf("get: %d %s", rec.Code, rec.Body)
	}
	if rec := th.do("GET", "/api/archives/9", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", rec.Code)
	}
}

func TestRemoveArchive(t *testing.T) {
	fc := &fakeClient{}
	th := newTestHandler(t, fc)
	if rec := th.do("DELETE", "/api/archives/7", `{}`, nil); rec.Code != http.StatusNoContent || len(fc.removed) != 1 || fc.removed[0] != "archive/7" {
		t.Errorf("remove: %d %v", rec.Code, fc.removed)
	}
	fc.err = status.Error(codes.NotFound, "no such archive")
	if rec := th.do("DELETE", "/api/archives/7", `{}`, nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing: %d", rec.Code)
	}
}

// TestArchiveFileHeaders pins the headers that keep an archive (untrusted
// HTML) from ever being rendered on the app's origin.
func TestArchiveFileHeaders(t *testing.T) {
	fc := &fakeClient{}
	a := fc.arch()
	a.archives = []*pb.Archive{{Id: 4, Status: "done", Title: `../Evil "Title"; <script>.html`, ContentType: "text/html; charset=utf-8", Size: 10}}
	a.chunks = [][]byte{[]byte("<html><script>alert(1)</script>"), []byte("</html>")}
	th := newTestHandler(t, fc)

	rec := th.do("GET", "/api/archives/4/file", "", nil)
	if rec.Code != 200 {
		t.Fatalf("download: %d %s", rec.Code, rec.Body)
	}
	if got := rec.Body.String(); got != "<html><script>alert(1)</script></html>" {
		t.Errorf("body = %q", got)
	}
	h := rec.Header()
	for k, want := range map[string]string{
		"Content-Type":            "application/octet-stream",
		"Content-Security-Policy": "sandbox; default-src 'none'",
		"X-Content-Type-Options":  "nosniff",
		"Cache-Control":           "private, no-store",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	typ, params, err := mime.ParseMediaType(h.Get("Content-Disposition"))
	if err != nil || typ != "attachment" {
		t.Fatalf("Content-Disposition = %q (%v)", h.Get("Content-Disposition"), err)
	}
	if got := params["filename"]; got != "evil-title-script-html.html" {
		t.Errorf("filename = %q", got)
	}
}

func TestArchiveFileNames(t *testing.T) {
	for _, tc := range []struct {
		ar   *pb.Archive
		want string
	}{
		{&pb.Archive{Id: 2, Title: "Hello, World!", ContentType: "text/html; charset=utf-8"}, "hello-world.html"},
		{&pb.Archive{Id: 2, Title: "Report", ContentType: "application/pdf"}, "report.pdf"},
		{&pb.Archive{Id: 2, Title: "日本語", ContentType: "application/pdf"}, "archive-2.pdf"},
		{&pb.Archive{Id: 2, Title: "a/b\\c\r\nd", ContentType: ""}, "a-b-c-d.html"},
		{&pb.Archive{Id: 2, Title: strings.Repeat("x", 500)}, strings.Repeat("x", 80) + ".html"},
	} {
		if got := archiveFileName(tc.ar); got != tc.want {
			t.Errorf("archiveFileName(%q) = %q, want %q", tc.ar.Title, got, tc.want)
		}
	}
}

func TestArchiveFileErrors(t *testing.T) {
	fc := &fakeClient{}
	a := fc.arch()
	a.archives = []*pb.Archive{{Id: 4, Status: "failed"}}
	a.contentErr = status.Error(codes.FailedPrecondition, "archive is failed")
	th := newTestHandler(t, fc)

	rec := th.do("GET", "/api/archives/4/file", "", nil)
	if rec.Code != http.StatusBadRequest || strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("not done: %d %v", rec.Code, rec.Header())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("error Content-Type = %q", ct)
	}
	if rec := th.do("GET", "/api/archives/9/file", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", rec.Code)
	}
	if rec := th.do("GET", "/api/archives/x/file", "", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", rec.Code)
	}
}
