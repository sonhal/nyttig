package archiver

import "time"

// Limits bounds one archive job.
type Limits struct {
	MaxPageBytes  int64         // the page itself (10 MiB)
	MaxAssetBytes int64         // each asset (8 MiB)
	MaxTotalBytes int64         // everything, before compression (50 MiB)
	MaxAssets     int           // 300
	Parallel      int           // asset fetches at once (4)
	Deadline      time.Duration // the whole job (3 minutes)
}

// DefaultLimits returns the limits a job gets unless configured otherwise.
func DefaultLimits() Limits {
	return Limits{
		MaxPageBytes:  10 << 20,
		MaxAssetBytes: 8 << 20,
		MaxTotalBytes: 50 << 20,
		MaxAssets:     300,
		Parallel:      4,
		Deadline:      3 * time.Minute,
	}
}

// Result is a saved page: self-contained HTML, or a PDF as fetched.
type Result struct {
	ContentType   string // "text/html; charset=utf-8" or "application/pdf"
	FinalURL      string // after redirects
	HTTPStatus    int
	Body          []byte
	MissingAssets int
	Note          string // set when the page has little visible text
	TextLength    int    // visible text, in characters
}
