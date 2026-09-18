// Package migrations embeds the SQL migration files into the binary so the
// service can migrate itself — no separate image or mounted directory needed.
package migrations

import "embed"

// FS holds every .sql migration in this directory.
//
//go:embed *.sql
var FS embed.FS
