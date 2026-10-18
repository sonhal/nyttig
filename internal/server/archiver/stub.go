package archiver

import (
	"context"
	"errors"
	"net/http"
)

// Archive is a stub that always fails. It is replaced by archive.go when
// Phase 1 (the real archiver) is merged; delete this file then.
func Archive(ctx context.Context, client *http.Client, pageURL string, lim Limits) (*Result, error) {
	return nil, errors.New("archiver is not implemented yet")
}
