// Package db embeds the goose migrations so the binary can self-migrate on
// startup (and so tests always run against the real schema).
package db

import "embed"

//go:embed migrations/*.sql
var Migrations embed.FS
