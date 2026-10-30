// Package migrations holds the database schema, as numbered SQL files that are
// compiled into the binary and applied when the server starts.
package migrations

import "embed"

// FS contains the migration files, in the format the goose library reads.
//
//go:embed *.sql
var FS embed.FS
