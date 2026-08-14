// Package migrations embeds the Trilli database migration SQL files so the
// trilli binary can apply them at runtime via the trilli `migrate` subcommand.
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
