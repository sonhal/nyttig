package service

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/fetcher"
)

func TestAddSource_EUVD(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	src, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "euvd", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if src.Url != fetcher.EUVDDefaultURL || src.Name != fetcher.EUVDDefaultName || src.Type != "euvd" {
		t.Errorf("source = %q %q %q", src.Url, src.Name, src.Type)
	}
	// A second default source reads the same search.
	_, err = svc.AddSource(ctx, &pb.AddSourceRequest{Type: "euvd", Url: "  "})
	wantCode(t, err, codes.AlreadyExists)

	critical := "https://euvdservices.enisa.europa.eu/api/search?fromScore=9"
	src2, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "euvd", Url: critical, Name: "EUVD critical"})
	if err != nil || src2.Url != critical || src2.Name != "EUVD critical" {
		t.Fatalf("custom: %v %v", src2, err)
	}

	for _, bad := range []string{"ftp://x/api/search", "not a url", "https://user:pw@x/api/search"} {
		_, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "euvd", Url: bad})
		wantCode(t, err, codes.InvalidArgument)
	}
}

func TestUpdateSource_EUVD(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	src, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "euvd", Url: "https://mirror.example/api/search"})
	if err != nil {
		t.Fatal(err)
	}
	blank := ""
	got, err := svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Url: &blank})
	if err != nil || got.Url != fetcher.EUVDDefaultURL {
		t.Fatalf("blank url: %v %v", got, err)
	}
	// A blank url is only allowed for euvd.
	rss := "rss"
	_, err = svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Url: &blank, Type: &rss})
	wantCode(t, err, codes.InvalidArgument)
	// The name still can't be cleared by an update.
	_, err = svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Name: &blank})
	wantCode(t, err, codes.InvalidArgument)
}
