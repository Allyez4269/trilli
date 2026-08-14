// Package migrations embeds the CMX database migration SQL files so the
// trilli-cmx binary can apply them at runtime via the `migrate` subcommand.
//
// CMX migrations live in the SHARED TRILLI database but are version-tracked in
// their own bookkeeping table (cmx_schema_migrations, see
// system/database/postgres/migrate.go) so they never collide with the app's
// migration state. Every CMX-owned object is prefixed `cmx_`.
//
// New migrations are added as a pair of files:
//
//	NNNNNN_<name>.up.sql   -- forward migration
//	NNNNNN_<name>.down.sql -- rollback
//
// where NNNNNN is a 6-digit zero-padded sequence number. golang-migrate sorts
// migrations by that prefix.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
