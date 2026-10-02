package main

import (
	"fmt"
	"reflect"
	"testing"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func TestTagTreeRows(t *testing.T) {
	tag := func(id int64, name string, parents ...int64) *pb.Tag {
		return &pb.Tag{Id: id, Name: name, ParentIds: parents}
	}
	for _, tc := range []struct {
		name string
		tags []*pb.Tag
		want []string
	}{
		{"flat", []*pb.Tag{tag(1, "a"), tag(2, "b")}, []string{"0 a", "0 b"}},
		{"nested", []*pb.Tag{tag(1, "a"), tag(2, "b", 1), tag(3, "c", 2)}, []string{"0 a", "1 b", "2 c"}},
		{
			"diamond appears under each parent",
			[]*pb.Tag{tag(1, "cyber security"), tag(2, "linux"), tag(3, "CVE", 1), tag(4, "linux security", 1, 2)},
			[]string{
				"0 cyber security", "1 CVE", "1 linux security (also under: linux)",
				"0 linux", "1 linux security (also under: cyber security)",
			},
		},
		{"unknown parent is a root", []*pb.Tag{tag(1, "a", 99)}, []string{"0 a"}},
		{"cycle is cut", []*pb.Tag{tag(1, "a", 2), tag(2, "b", 1)}, nil},
		{"cycle below a root is cut", []*pb.Tag{tag(1, "r"), tag(2, "a", 1, 3), tag(3, "b", 2)}, []string{"0 r", "1 a (also under: b)", "2 b"}},
		{"empty", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, r := range tagTreeRows(tc.tags) {
				line := fmt.Sprintf("%d %s", r.Depth, r.Tag.Name)
				if len(r.AlsoUnder) > 0 {
					line += fmt.Sprintf(" (also under: %s)", r.AlsoUnder[0])
					for _, o := range r.AlsoUnder[1:] {
						line += ", " + o
					}
				}
				got = append(got, line)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}
