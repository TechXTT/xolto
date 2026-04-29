// Package migrations embeds all SQL migration files so the server binary
// can apply them without relying on the filesystem layout of the container.
//
// The embed.FS is consumed by internal/store via the golang-migrate iofs driver.
// SQLite (dev/test) does NOT use this FS — it keeps its own inline migrate* helpers.
package migrations

import "embed"

// FS contains every *.up.sql and *.down.sql file in this directory.
// The iofs driver is keyed on file names of the form NNNNNN_description.up.sql.
//
//go:embed *.sql
var FS embed.FS
