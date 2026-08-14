# trilli-cmx

The Trilli operator console ("CMX") — the application for the people *running*
a Trilli deployment. A separate Go service with its own embedded React
interface, mirroring the platform's architecture.

Built and maintained by **Michael Bahlitzanakis**. MIT licensed.

## What it does

- **Accounts** — browse tenants, suspend/reactivate, quota overrides, plan
  changes, lifecycle state.
- **Catalog** — create and manage the plan catalog (pricing, allowances,
  availability).
- **Comps** — issue and revoke complimentary/ambassador account invites.
- **Revenue** — billing transactions, refunds, credits, revenue reporting.
- **Support** — triage and reply to customer support tickets.
- **Infrastructure** — run background jobs on demand, trigger storage-tiering
  passes, inspect job coordination.

Operators are their own principal type (not app users) with password + TOTP
two-factor sign-in and step-up confirmation for sensitive actions.

## How it integrates

CMX is a second service on the **same Postgres database** as the platform
(its migrations are tracked in a separate `cmx_schema_migrations` table so the
two never fight). Reads go straight to the database; **all mutations go through
the platform's `/api/admin/*` surface**, authenticated by a shared service
token, so application invariants live in exactly one place.

Provision that token on the platform side:

```bash
trilli creds set cmx service_token live <random-token>
```

## Running it

Uses the same `TRILLI_DB_*`, `TRILLI_ROOT_KEY`, and `TRILLI_TLS_*` environment
variables as the platform (see [`.env.example`](.env.example)):

```bash
cp .env.example cmx.env      # edit it
set -a; source cmx.env; set +a

cd interface && npm install && npm run build && cd ..
./build.sh                   # go build -> bin/trilli-cmx
./bin/trilli-cmx
```

Set `CMX_DEBUG=1` for debug-level logs.

## Layout

```
cmd/trilli-cmx/    entry point + composition root
system/<domain>/   admin, appadmin (platform API client), auth, catalog, comp,
                   directory, infra, operators, reports, revenue, support, …
interface/         React + TypeScript + Vite operator UI (embedded at build)
```
