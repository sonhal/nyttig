package archiver

import (
	"strconv"
	"strings"
)

type srcCandidate struct {
	url string
	w   float64 // 0 when the descriptor is not a width
	x   float64 // density; 1 when absent
}

const (
	maxPickWidth   = 1600
	maxPickDensity = 2
)

// parseSrcset splits a srcset attribute into candidates. A URL may contain
// commas; descriptors run to the next comma.
func parseSrcset(s string) []srcCandidate {
	var out []srcCandidate
	for i := 0; i < len(s); {
		for i < len(s) && (isSpace(s[i]) || s[i] == ',') {
			i++
		}
		start := i
		for i < len(s) && !isSpace(s[i]) {
			i++
		}
		u := s[start:i]
		if u == "" {
			break
		}
		cand := srcCandidate{x: 1}
		if strings.HasSuffix(u, ",") {
			cand.url = strings.TrimRight(u, ",")
		} else {
			cand.url = u
			end := strings.IndexByte(s[i:], ',')
			desc := s[i:]
			if end >= 0 {
				desc = s[i : i+end]
				i += end + 1
			} else {
				i = len(s)
			}
			for _, d := range strings.Fields(desc) {
				n, err := strconv.ParseFloat(d[:len(d)-1], 64)
				if err != nil || n <= 0 {
					continue
				}
				switch d[len(d)-1] {
				case 'w':
					cand.w = n
				case 'x':
					cand.x = n
				}
			}
		}
		if cand.url != "" {
			out = append(out, cand)
		}
	}
	return out
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

// pickSrcset chooses one candidate: with widths, the largest at or under
// 1600 else the smallest; with densities, the largest at or under 2x else the
// smallest. It returns "" when there is none.
func pickSrcset(s string) string {
	cands := parseSrcset(s)
	if len(cands) == 0 {
		return ""
	}
	var widths []srcCandidate
	for _, c := range cands {
		if c.w > 0 {
			widths = append(widths, c)
		}
	}
	key, limit := func(c srcCandidate) float64 { return c.x }, float64(maxPickDensity)
	if len(widths) > 0 {
		cands = widths
		key, limit = func(c srcCandidate) float64 { return c.w }, maxPickWidth
	}
	best, smallest := -1, 0
	for i, c := range cands {
		if key(c) < key(cands[smallest]) {
			smallest = i
		}
		if key(c) <= limit && (best < 0 || key(c) > key(cands[best])) {
			best = i
		}
	}
	if best < 0 {
		best = smallest
	}
	return cands[best].url
}
