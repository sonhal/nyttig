package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/db"
	"github.com/sonhal/nyttig/internal/server/fetcher"
)

const aliceDID = "did:plc:z72i7hdynmk6r22z27h6tvur"

// fakeResolver knows a fixed set of accounts, by handle and by DID.
type fakeResolver struct {
	profiles map[string]*fetcher.BlueskyProfile
	err      error // returned for every lookup when set
	asked    []string
}

func (f *fakeResolver) ResolveProfile(_ context.Context, actor string) (*fetcher.BlueskyProfile, error) {
	f.asked = append(f.asked, actor)
	if f.err != nil {
		return nil, f.err
	}
	if p, ok := f.profiles[actor]; ok {
		return p, nil
	}
	return nil, &fetcher.XRPCError{Status: 400, Code: "InvalidRequest", Message: "Profile not found"}
}

func newBlueskyService(t *testing.T) (*Service, *fakeResolver) {
	t.Helper()
	svc, _ := newTestService(t)
	alice := &fetcher.BlueskyProfile{DID: aliceDID, Handle: "alice.bsky.social", DisplayName: "Alice"}
	bob := &fetcher.BlueskyProfile{DID: "did:plc:bob", Handle: "bob.example.com"} // no display name
	fr := &fakeResolver{profiles: map[string]*fetcher.BlueskyProfile{
		"alice.bsky.social": alice, aliceDID: alice, "bob.example.com": bob, "did:plc:bob": bob,
	}}
	svc.SetProfileResolver(fr)
	return svc, fr
}

func TestAddSource_BlueskyInputForms(t *testing.T) {
	inputs := []string{
		"alice.bsky.social",
		"@alice.bsky.social",
		"  @Alice.Bsky.Social ",
		"https://bsky.app/profile/alice.bsky.social",
		"https://bsky.app/profile/" + aliceDID,
		aliceDID,
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			svc, fr := newBlueskyService(t)
			src, err := svc.AddSource(context.Background(), &pb.AddSourceRequest{Type: "bluesky", Url: in, Enabled: true})
			if err != nil {
				t.Fatalf("AddSource(%q): %v", in, err)
			}
			if src.Url != "https://bsky.app/profile/"+aliceDID {
				t.Errorf("url = %q, want the DID profile URL", src.Url)
			}
			if src.Name != "Alice" || src.Type != "bluesky" {
				t.Errorf("name/type = %q/%q, want Alice/bluesky", src.Name, src.Type)
			}
			if len(fr.asked) != 1 {
				t.Errorf("resolver called %d times, want once", len(fr.asked))
			}
		})
	}
}

func TestAddSource_BlueskyNames(t *testing.T) {
	svc, _ := newBlueskyService(t)
	ctx := context.Background()

	src, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: "bob.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if src.Name != "@bob.example.com" {
		t.Errorf("name = %q, want @handle when there is no display name", src.Name)
	}

	src, err = svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: "alice.bsky.social", Name: "My friend"})
	if err != nil {
		t.Fatal(err)
	}
	if src.Name != "My friend" {
		t.Errorf("name = %q, want the given name kept", src.Name)
	}
}

func TestAddSource_NameStillRequiredForOtherTypes(t *testing.T) {
	svc, _ := newBlueskyService(t)
	for _, typ := range []string{"", "rss", "atom"} {
		_, err := svc.AddSource(context.Background(), &pb.AddSourceRequest{Type: typ, Url: "https://a.example/feed"})
		wantCode(t, err, codes.InvalidArgument)
	}
}

func TestAddSource_BlueskyErrors(t *testing.T) {
	ctx := context.Background()

	for _, in := range []string{"", "alice", "alice..bsky.social", "https://example.com/profile/alice.bsky.social", "did:plc:", "alice.bsky.social/x"} {
		svc, fr := newBlueskyService(t)
		_, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: in})
		wantCode(t, err, codes.InvalidArgument)
		if len(fr.asked) != 0 {
			t.Errorf("%q: resolver called for invalid syntax", in)
		}
	}

	t.Run("not found", func(t *testing.T) {
		svc, _ := newBlueskyService(t)
		_, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: "nobody.bsky.social"})
		wantCode(t, err, codes.InvalidArgument)
		if got := err.Error(); !strings.Contains(got, "Profile not found") {
			t.Errorf("error %q does not carry Bluesky's message", got)
		}
		list, _ := svc.ListSources(ctx, nil)
		if len(list.Sources) != 0 {
			t.Error("a failed add saved a source")
		}
	})

	t.Run("network failure", func(t *testing.T) {
		svc, fr := newBlueskyService(t)
		fr.err = errors.New("dial tcp: connection refused")
		_, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: "alice.bsky.social"})
		wantCode(t, err, codes.Unavailable)
		list, _ := svc.ListSources(ctx, nil)
		if len(list.Sources) != 0 {
			t.Error("a failed add saved a source")
		}
	})

	t.Run("rate limited", func(t *testing.T) {
		svc, fr := newBlueskyService(t)
		fr.err = &fetcher.XRPCError{Status: 429, Code: "RateLimitExceeded"}
		_, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: "alice.bsky.social"})
		wantCode(t, err, codes.Unavailable)
	})

	t.Run("server error", func(t *testing.T) {
		svc, fr := newBlueskyService(t)
		fr.err = &fetcher.XRPCError{Status: 502}
		_, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: "alice.bsky.social"})
		wantCode(t, err, codes.Unavailable)
	})

	t.Run("no resolver", func(t *testing.T) {
		svc, _ := newTestService(t)
		_, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: "alice.bsky.social"})
		wantCode(t, err, codes.Unavailable)
	})
}

func TestAddSource_BlueskyDuplicateAccount(t *testing.T) {
	svc, _ := newBlueskyService(t)
	ctx := context.Background()
	if _, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: "alice.bsky.social"}); err != nil {
		t.Fatal(err)
	}
	// The same account by another route resolves to the same DID URL.
	_, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: aliceDID})
	wantCode(t, err, codes.AlreadyExists)
}

func TestUpdateSource_Bluesky(t *testing.T) {
	ctx := context.Background()

	t.Run("changing only the type resolves the existing url", func(t *testing.T) {
		svc, fr := newBlueskyService(t)
		src := addSource(t, svc, "Alice feed", "https://bsky.app/profile/alice.bsky.social")
		typ := "bluesky"
		got, err := svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Type: &typ})
		if err != nil {
			t.Fatal(err)
		}
		if got.Type != "bluesky" || got.Url != "https://bsky.app/profile/"+aliceDID || got.Name != "Alice feed" {
			t.Errorf("source = type %q url %q name %q", got.Type, got.Url, got.Name)
		}
		if len(fr.asked) != 1 || fr.asked[0] != "alice.bsky.social" {
			t.Errorf("resolver asked %v", fr.asked)
		}
	})

	t.Run("changing the url of a bluesky source resolves it", func(t *testing.T) {
		svc, _ := newBlueskyService(t)
		src, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: "alice.bsky.social"})
		if err != nil {
			t.Fatal(err)
		}
		url := "@bob.example.com"
		got, err := svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Url: &url})
		if err != nil {
			t.Fatal(err)
		}
		if got.Url != "https://bsky.app/profile/did:plc:bob" {
			t.Errorf("url = %q", got.Url)
		}
	})

	t.Run("an unrelated edit does not resolve", func(t *testing.T) {
		svc, fr := newBlueskyService(t)
		src, _ := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: "alice.bsky.social"})
		fr.asked = nil
		name, sameURL, sameType := "Renamed", src.Url, "bluesky"
		if _, err := svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Name: &name, Url: &sameURL, Type: &sameType}); err != nil {
			t.Fatal(err)
		}
		if len(fr.asked) != 0 {
			t.Errorf("resolver asked %v for an edit that left url and type alone", fr.asked)
		}
	})

	t.Run("a failed lookup leaves the source unchanged", func(t *testing.T) {
		svc, _ := newBlueskyService(t)
		src := addSource(t, svc, "Feed", "https://bsky.app/profile/nobody.bsky.social")
		typ := "bluesky"
		_, err := svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Type: &typ})
		wantCode(t, err, codes.InvalidArgument)
		list, _ := svc.ListSources(ctx, nil)
		if list.Sources[0].Type != "rss" {
			t.Errorf("type = %q, want rss", list.Sources[0].Type)
		}
	})

	t.Run("a bluesky url is rejected for rss, and a feed url for bluesky", func(t *testing.T) {
		svc, _ := newBlueskyService(t)
		src := addSource(t, svc, "Feed", "https://a.example/feed")
		url := "alice.bsky.social"
		_, err := svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Url: &url})
		wantCode(t, err, codes.InvalidArgument)
		typ := "bluesky"
		_, err = svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Type: &typ})
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("the name stays required", func(t *testing.T) {
		svc, _ := newBlueskyService(t)
		src, _ := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: "alice.bsky.social"})
		name := " "
		_, err := svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Name: &name})
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("duplicate account", func(t *testing.T) {
		svc, _ := newBlueskyService(t)
		if _, err := svc.AddSource(ctx, &pb.AddSourceRequest{Type: "bluesky", Url: "alice.bsky.social"}); err != nil {
			t.Fatal(err)
		}
		other := addSource(t, svc, "Feed", "https://bsky.app/profile/alice.bsky.social")
		typ := "bluesky"
		_, err := svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: other.Id, Type: &typ})
		wantCode(t, err, codes.AlreadyExists)
	})
}

func TestUpdateSource_TypeChangeNotifiesScheduler(t *testing.T) {
	svc, _ := newBlueskyService(t)
	src := addSource(t, svc, "Feed", "https://bsky.app/profile/alice.bsky.social")
	notified := 0
	svc.OnSourceUpdated(func(context.Context, db.Source) { notified++ })
	typ := "bluesky"
	if _, err := svc.UpdateSource(context.Background(), &pb.UpdateSourceRequest{Id: src.Id, Type: &typ}); err != nil {
		t.Fatal(err)
	}
	if notified != 1 {
		t.Errorf("scheduler notified %d times, want 1", notified)
	}
}
