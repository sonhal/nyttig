package service

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/db"
	"github.com/sonhal/nyttig/internal/server/fetcher"
)

func TestAddSource_KEVDefaults(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	src, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "kev", Enabled: true})
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}
	if src.Url != fetcher.KEVDefaultURL || src.Name != fetcher.KEVDefaultName || src.Type != "kev" {
		t.Errorf("source = url %q name %q type %q, want the defaults", src.Url, src.Name, src.Type)
	}

	// The default URL is stored, so a second default source is a duplicate.
	_, err = svc.AddSource(ctx, &pb.AddSourceRequest{Type: "kev", Name: "  ", Url: " "})
	wantCode(t, err, codes.AlreadyExists)
}

func TestAddSource_KEVCustom(t *testing.T) {
	svc, _ := newTestService(t)
	mirror := "https://raw.githubusercontent.com/cisagov/kev-data/main/known_exploited_vulnerabilities.json"
	src, err := svc.AddSource(context.Background(), &pb.AddSourceRequest{Type: "kev", Name: "KEV mirror", Url: mirror})
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}
	if src.Url != mirror || src.Name != "KEV mirror" {
		t.Errorf("source = url %q name %q", src.Url, src.Name)
	}
}

func TestAddSource_KEVInvalidURL(t *testing.T) {
	svc, _ := newTestService(t)
	for _, u := range []string{"ftp://cisa.example/kev.json", "not a url", "javascript:alert(1)"} {
		_, err := svc.AddSource(context.Background(), &pb.AddSourceRequest{Type: "kev", Url: u})
		wantCode(t, err, codes.InvalidArgument)
	}
}

func TestAddSource_BlankStillInvalidForFeeds(t *testing.T) {
	svc, _ := newTestService(t)
	for _, typ := range []string{"rss", "atom"} {
		_, err := svc.AddSource(context.Background(), &pb.AddSourceRequest{Type: typ, Name: "Feed"})
		wantCode(t, err, codes.InvalidArgument)
		_, err = svc.AddSource(context.Background(), &pb.AddSourceRequest{Type: typ, Url: "https://a.example/feed"})
		wantCode(t, err, codes.InvalidArgument)
	}
}

func TestUpdateSource_KEV(t *testing.T) {
	ctx := context.Background()

	t.Run("blank url stores the default", func(t *testing.T) {
		svc, _ := newTestService(t)
		src, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "kev", Url: "https://mirror.example/kev.json"})
		if err != nil {
			t.Fatal(err)
		}
		got, err := svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Url: proto.String("")})
		if err != nil {
			t.Fatalf("UpdateSource: %v", err)
		}
		if got.Url != fetcher.KEVDefaultURL {
			t.Errorf("url = %q, want the default", got.Url)
		}
	})

	t.Run("an rss source becomes kev with a blank url", func(t *testing.T) {
		svc, _ := newTestService(t)
		src := addSource(t, svc, "Feed", "https://a.example/feed")
		var notified int
		svc.OnSourceUpdated(func(context.Context, db.Source) { notified++ })
		got, err := svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Type: proto.String("kev"), Url: proto.String("")})
		if err != nil {
			t.Fatalf("UpdateSource: %v", err)
		}
		if got.Type != "kev" || got.Url != fetcher.KEVDefaultURL || got.Name != "Feed" {
			t.Errorf("source = type %q url %q name %q", got.Type, got.Url, got.Name)
		}
		if notified != 1 {
			t.Errorf("scheduler notified %d times, want once", notified)
		}
	})

	t.Run("blank url is invalid once the type is rss", func(t *testing.T) {
		svc, _ := newTestService(t)
		src, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "kev"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Type: proto.String("rss"), Url: proto.String("")})
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("a blank name in a patch is still invalid", func(t *testing.T) {
		svc, _ := newTestService(t)
		src, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "kev"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Name: proto.String("")})
		wantCode(t, err, codes.InvalidArgument)
	})
}

func TestValidateSourceFields_KEV(t *testing.T) {
	tests := []struct {
		typ, name, url string
		ok             bool
	}{
		{"kev", "", "", true},
		{"kev", "Mine", "https://a.example/kev.json", true},
		{"kev", "", "ftp://a.example/kev.json", false},
		{"rss", "", "https://a.example/feed", false},
		{"rss", "Feed", "", false},
	}
	for _, tc := range tests {
		err := firstErr(validateFeedType(tc.typ), validateSourceName(tc.typ, tc.name), validateSourceURL(tc.typ, tc.url))
		if (err == nil) != tc.ok {
			t.Errorf("%s name %q url %q: err = %v, want ok=%v", tc.typ, tc.name, tc.url, err, tc.ok)
		}
	}
}
