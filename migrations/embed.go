// Package migrations embeds the SQL migration files so services can apply them
// at startup, and the goose CLI can apply them from disk during development.
package migrations

import "embed"

// FS holds the embedded *.sql migration files.
//
//go:embed *.sql
var FS embed.FS
