# Contributing to Trilli

Thanks for your interest! Contributions are welcome — bug reports, fixes,
documentation, and features that fit the project's philosophy: one binary,
explicit wiring, boring patterns.

## Ground rules

- **Match the existing patterns.** Every backend domain is a package under
  `system/` with a `service.go` (business logic, raw SQL) and `handlers.go`
  (HTTP + a `Register` method). Read a neighboring package before writing a
  new one.
- **Frontend follows the design spec.** `trilli-serve/TRILLI-DESIGN-SPEC.md`
  documents the design language and React conventions — new UI should be
  indistinguishable from existing surfaces.
- **Migrations are append-only.** Add a new `NNNNNN_name.{up,down}.sql` pair
  under `system/database/migrations/`; never edit an applied migration.
- **Comments explain constraints, not mechanics.**

## Before you open a PR

```bash
# backend
go build ./... && go test ./...

# frontend
cd interface && npx tsc --noEmit && npm run build
```

Use conventional commit messages (`feat(files): …`, `fix(sign): …`,
`docs: …`) and keep each PR to one logical change.

## Questions & ideas

Open a GitHub issue or discussion. For security matters, see
[SECURITY.md](SECURITY.md) — never a public issue.
