package client

import (
	"context"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func TestEnsureDigestSeries_CreatesThenFinds(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()

	id, err := c.EnsureDigestSeries(context.Background(), 3, "daily-cve", "CVE news")
	if err != nil || id != 1 {
		t.Fatalf("EnsureDigestSeries = %d, %v; want the new series' id 1", id, err)
	}
	if s := ts.series[0]; s.Name != "daily-cve" || s.Description != "CVE news" || s.AssessorId != 3 {
		t.Errorf("created %+v", s)
	}
	// Found again, in any case, without creating another.
	id, err = c.EnsureDigestSeries(context.Background(), 3, "DAILY-CVE", "")
	if err != nil || id != 1 || len(ts.series) != 1 {
		t.Errorf("second EnsureDigestSeries = %d, %v with %d series", id, err, len(ts.series))
	}
	// Another assessor's series with the same name is a different series.
	id, err = c.EnsureDigestSeries(context.Background(), 4, "daily-cve", "")
	if err != nil || id != 2 || len(ts.series) != 2 {
		t.Errorf("other assessor: %d, %v with %d series", id, err, len(ts.series))
	}
}

func TestEnsureDigestSeries_UsesOneAnotherClientCreated(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()
	ts.seriesRace = &pb.DigestSeries{Id: 9, AssessorId: 3, Name: "Daily-CVE"}

	id, err := c.EnsureDigestSeries(context.Background(), 3, "daily-cve", "")
	if err != nil || id != 9 {
		t.Fatalf("EnsureDigestSeries = %d, %v; want the other client's series 9", id, err)
	}
}

func TestEnsureDigestSeries_Racing(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()

	var wg sync.WaitGroup
	ids := make([]int64, 8)
	errs := make([]error, 8)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], errs[i] = c.EnsureDigestSeries(context.Background(), 3, "monthly", "")
		}()
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("call %d = %d, %v; all calls should get the same series (%d)", i, ids[i], errs[i], ids[0])
		}
	}
	if len(ts.series) != 1 {
		t.Fatalf("%d series created, want 1", len(ts.series))
	}
}

func TestEnsureDigestSeries_OtherErrorsPass(t *testing.T) {
	c, _, cleanup := setupTest(t)
	defer cleanup()
	_, err := c.EnsureDigestSeries(context.Background(), 3, strings.Repeat("x", 100), "")
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}
