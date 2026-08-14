# Trilli

**A complete, self-hosted cloud storage & collaboration platform — in one Go binary.**

Trilli is what you get when one person decides that secure file storage, sharing,
an office suite, a PDF toolkit, e-signatures, and the entire SaaS business layer
that runs them should live in a single, understandable codebase. No Kubernetes,
no microservice sprawl, no framework magic — a Go binary, a Postgres database, a
blob store, and a React frontend embedded right into the executable.

It powers [trilli.com](https://trilli.com) in production. Today it's yours.

## What's inside

| | |
|---|---|
| **Encrypted file storage** | Accounts → workspaces → folders → files, with per-tenant AES-256-GCM encryption at rest — the cloud provider only ever sees ciphertext. Resumable chunked uploads, trash with retention, thumbnails, previews, adaptive hot/cool/cold storage tiering. |
| **Sharing & collaboration** | Share links (member, email, or public) with passwords and expiry, public download pages, and inbound "drop portals" for collecting files from anyone. |
| **Office suite** | Docs, Sheets & Slides editing in the browser via Collabora/WOPI, with live co-editing, presence, and export to legacy formats. |
| **PDF toolkit** | Merge, split, compress, protect, convert — a full PDF tool suite built on pdfcpu and pdfium, running in-process. |
| **E-signature** | DocuSign-style envelopes: field placement, ordered signing ceremonies, audit trails with geolocation, a rendered certificate of completion, and executed documents sealed with a PKCS#7 digital signature. |
| **The business layer** | Stripe subscriptions with a pay-before-account signup funnel, seat management, plan catalog, transfer metering, account lifecycle (lapse → warn → purge), support desk, audit log, programmatic API with keys. |
| **Security stack** | Argon2id passwords, TOTP 2FA with trusted devices, WebAuthn passkeys, Google/Microsoft sign-in, verified password reset, session geolocation, in-process TLS termination with hot certificate reload. |

## The two applications

- **[`trilli-serve/`](trilli-serve/)** — the platform: API + embedded web app,
  background jobs, migrations, and an operator CLI. This is the product.
- **[`trilli-cmx/`](trilli-cmx/)** — the operator console: a separate service
  for the people *running* a Trilli deployment — accounts, plans, comp invites,
  revenue reporting, support triage, and infrastructure jobs.

Each directory has its own README with full setup instructions.

## Design philosophy

- **One binary per service.** The React app is compiled into the Go executable
  (`go:embed`). Deploy = build + restart.
- **No web framework.** Plain `net/http` and hand-wired dependency injection in
  a single composition root you can read top to bottom.
- **Postgres is the truth.** 90+ embedded migrations; advisory locks coordinate
  background jobs across replicas — no external queue or scheduler.
- **Boring, explicit patterns.** Every domain is a package with a `service.go`
  and a `handlers.go`. If you've read one, you can navigate all of them.

## Quick start

```bash
# 1. The platform
cd trilli-serve
cp .env.example trilli.env          # fill in your database + storage credentials
set -a; source trilli.env; set +a
go run ./cmd/trilli migrate up
go run ./cmd/trilli

# 2. The operator console (optional)
cd ../trilli-cmx
./build.sh && ./bin/trilli-cmx
```

See [`trilli-serve/README.md`](trilli-serve/README.md) for the complete
configuration reference and production deployment guide.

## License & credits

Trilli was designed and built by **Michael Bahlitzanakis**.

Released under the [MIT License](LICENSE). The Trilli name and logo identify
the original project and its hosted service; if you fork and operate your own
service, please run it under your own name.

Security reports: please use GitHub's private vulnerability reporting — see
[SECURITY.md](SECURITY.md).
