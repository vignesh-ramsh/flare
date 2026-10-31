// Package migrations holds the database schema, as numbered SQL files that are
// compiled into the binary and applied when the server starts.
package migrations

import "embed"

// FS contains the migration files of the server, in the format the goose library
// reads.
//
//go:embed *.sql
var FS embed.FS

// RelayFS contains those of a push relay, which has a database of its own, in the
// folder "relay".
//
//go:embed relay/*.sql
var RelayFS embed.FS
