package client

import (
	"context"
	"testing"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func TestEnsureMe_CreatesItOnFirstUse(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()
	ts.assessors = []*pb.Assessor{{Id: 1, Name: "claude"}}

	id, err := c.EnsureMe(context.Background())
	if err != nil || id != 2 {
		t.Fatalf("EnsureMe = %d, %v; want the new assessor's id 2", id, err)
	}
	me := ts.assessors[1]
	if me.Name != MeAssessorName || me.Description != MeAssessorDescription || me.Color != MeAssessorColor {
		t.Errorf("created %+v", me)
	}
	// The second time it is found, not created again.
	id, err = c.EnsureMe(context.Background())
	if err != nil || id != 2 || len(ts.assessors) != 2 {
		t.Errorf("second EnsureMe = %d, %v with %d assessors", id, err, len(ts.assessors))
	}
}

func TestEnsureMe_UsesOneAnotherClientCreated(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()
	ts.assessorRace = &pb.Assessor{Id: 9, Name: MeAssessorName}

	id, err := c.EnsureMe(context.Background())
	if err != nil || id != 9 {
		t.Fatalf("EnsureMe = %d, %v; want the other client's assessor 9", id, err)
	}
}

func TestRate_UsesMeForTheWholeItem(t *testing.T) {
	c, ts, cleanup := setupTest(t)
	defer cleanup()
	a, err := c.Rate(context.Background(), 7, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if a.ItemId != 7 || a.AssessorId != ts.assessors[0].Id || a.Score == nil || *a.Score != 0 || a.TagId != 0 {
		t.Errorf("Rate = %+v", a)
	}
	a, err = c.Rate(context.Background(), 8, 0.75, "good")
	if err != nil || a.Note != "good" || *a.Score != 0.75 || len(ts.assessors) != 1 {
		t.Errorf("Rate = %+v, %v (%d assessors)", a, err, len(ts.assessors))
	}
}
