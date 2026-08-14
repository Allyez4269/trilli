# trilli-serve

The Trilli platform: a multi-tenant cloud storage, collaboration, and
e-signature service — API, web app, background jobs, and operator CLI — in a
single Go binary.

Built and maintained by **Michael Bahlitzanakis**. MIT licensed.

## Architecture at a glance

- **Plain `net/http`** with Go 1.22 method-pattern routing. No framework. All
  services are constructed and wired by hand in one composition root:
  [`cmd/trilli/main.go`](cmd/trilli/main.go).
- **~45 domain packages** under `system/`, each following the same shape:
  `service.go` (business logic, raw SQL via pgx) + `handlers.go` (HTTP) + a
  `Register(...)` that mounts its routes behind the auth middleware it's given.
- **React 18 SPA** (`interface/`) compiled by Vite into `system/web/dist` and
  embedded into the binary with `go:embed`. One process serves API + app.
- **Postgres** holds all metadata; 90+ migrations are embedded and applied with
  `trilli migrate up`. Background jobs coordinate across replicas with advisory
  locks — no external queue.
- **Blob storage** (Azure Blob) holds file bytes, wrapped in a per-tenant
  AES-256-GCM encrypting layer so the storage provider only sees ciphertext.
  The master key lives in the database, wrapped by a root key you supply via
  the environment.
- **TLS is terminated in-process** with hot certificate reload, so the binary
  can serve HTTPS directly on its own IP.

## Requirements

| Component | Needed for | Notes |
|---|---|---|
| Go 1.25+ | building | |
| Node 18+ | building the frontend | only at build time; not at runtime |
| PostgreSQL 14+ | everything | |
| Azure Blob Storage account | file storage | the `storage.Store` interface is small if you want to write another backend |
| TLS certificate | serving | self-signed works for local dev (see below) |
| SendGrid account | transactional email | optional — app runs without it, sends fail loudly |
| Stripe account | billing | optional — billing endpoints return 503 until keys are stored in the vault |
| LibreOffice (headless) | Office→PDF previews, legacy export | optional |
| Collabora Online | in-browser Docs/Sheets/Slides editing | optional |
| poppler-utils (`pdftoppm`), ffmpeg | PDF/video thumbnails | optional |
| db-ip.com account | GeoIP auto-refresh | optional — attribution required per their free-tier license |

## Configuration

All configuration is via environment variables. Copy
[`.env.example`](.env.example) and fill it in:

| Variable | Required | Purpose |
|---|---|---|
| `TRILLI_DB_HOST` / `TRILLI_DB_PORT` / `TRILLI_DB_NAME` / `TRILLI_DB_USER` / `TRILLI_DB_PASSWORD` / `TRILLI_DB_SSLMODE` | yes | Postgres connection |
| `AZURE_STORAGE_ACCOUNT` / `AZURE_STORAGE_KEY` / `AZURE_STORAGE_CONTAINER` | yes | blob storage |
| `TRILLI_ROOT_KEY` | yes | 64 hex chars; wraps the master encryption key at rest in the DB (`openssl rand -hex 32`) |
| `APP_ENCRYPTION_KEY` | first boot | seeds the master key once; removable afterwards |
| `SENDGRID_API_KEY` / `TRILLI_MAIL_FROM` / `TRILLI_MAIL_FROM_NAME` | no | outbound email |
| `PORT` | no | HTTPS port (default 8081) |
| `TRILLI_TLS_CERT` / `TRILLI_TLS_KEY` | no | cert paths (default `certs/*.pem`) |
| `TRILLI_WOPI_LISTEN` | no | loopback listener for the Collabora engine (default `127.0.0.1:8090`) |
| `TRILLI_TIERING_MODE` | no | `off` / `dryrun` / `live` scheduled storage tiering |
| `TRILLI_GEOIP_KEY` / `TRILLI_DBIP_URL` | no | GeoIP lookup gate / db-ip download URL |
| `GOOGLE_OAUTH_REDIRECT` / `MICROSOFT_OAUTH_REDIRECT` / `GOOGLE_DRIVE_OAUTH_REDIRECT` | no | OAuth redirect overrides |
| `TRILLI_OFFICE_TEMPLATES` | no | directory of blank docx/xlsx/pptx templates (default `templates/office`) |

Third-party **service credentials** (Stripe keys, Google/Microsoft OAuth
clients, the operator-console service token, the e-signature sealing
certificate) are not environment variables — they live encrypted in the
database vault and are managed with the CLI:

```bash
trilli creds set stripe secret_key live sk_live_...
trilli creds list
```

## Running it

```bash
# certificates for local dev
mkdir -p certs && openssl req -x509 -newkey rsa:2048 -nodes -days 365 \
  -keyout certs/privkey.pem -out certs/fullchain.pem -subj "/CN=localhost"

# environment
cp .env.example trilli.env   # edit it
set -a; source trilli.env; set +a

# database
go run ./cmd/trilli migrate up

# frontend (rebuild whenever interface/ changes)
cd interface && npm install && npm run build && cd ..

# serve
go build -o bin/trilli ./cmd/trilli && ./bin/trilli
```

The app is now at `https://localhost:8081`.

## CLI

The same binary is the operator toolbox:

```
trilli                       Start the HTTP server daemon
trilli migrate up|down|version   Manage DB migrations
trilli creds set|list        Encrypted service-credentials vault
trilli stripe sync-plans     Sync the plan catalog to Stripe
trilli lifecycle status|sweep|purge   Lapsed-account management
trilli storage report|tier|encrypt-blobs   Storage analytics + tiering + encryption migration
trilli jobs status|ping      Background-job coordination
trilli key status|seed       Master-key management
trilli mail preview <email>  Send a sample of every transactional email
```

## Production deployment

A systemd unit is provided in [`deploy/trilli.service`](deploy/trilli.service):
binary at `/opt/trilli/bin/trilli`, environment in `/etc/trilli/trilli.env`.
Deploy is intentionally boring:

```bash
cd interface && npm run build && cd ..
go build -o bin/trilli ./cmd/trilli
sudo systemctl restart trilli
```

Multiple replicas can point at the same database and blob store — background
jobs elect a single runner per tick via Postgres advisory locks.

## Project layout

```
cmd/trilli/        entry point + composition root + CLI subcommands
system/<domain>/   one package per domain (auth, files, sharing, sign, billing, …)
system/database/   pgx client + embedded migrations
system/web/        the embedded SPA (go:embed of interface's build output)
interface/         React 18 + TypeScript + Vite + Tailwind frontend
deploy/            systemd unit
```

`TRILLI-DESIGN-SPEC.md` documents the UI design language and React conventions;
`DESIGN_SYSTEM.md` is the full, reusable design-system specification.
