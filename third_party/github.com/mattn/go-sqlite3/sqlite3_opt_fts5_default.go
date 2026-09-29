// Nyttig local addition (not part of upstream go-sqlite3).
//
// Nyttig's schema requires FTS5, so enable it by default. Upstream only
// enables it with the sqlite_fts5/fts5 build tags (sqlite3_opt_fts5.go);
// this file covers builds without those tags, and is excluded when either
// tag is set so the flags are never applied twice.

//go:build !sqlite_fts5 && !fts5

package sqlite3

/*
#cgo CFLAGS: -DSQLITE_ENABLE_FTS5
#cgo LDFLAGS: -lm
*/
import "C"
