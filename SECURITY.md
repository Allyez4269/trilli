# Security Policy

Trilli handles people's files, credentials, and payments — security reports are
taken seriously and appreciated.

## Reporting a vulnerability

Please **do not open a public issue** for security problems. Instead, use
GitHub's private vulnerability reporting ("Report a vulnerability" under the
repository's Security tab). You'll get an acknowledgment as quickly as
possible, typically within a few days.

Please include reproduction steps and the affected component
(`trilli-serve` / `trilli-cmx`), and give a reasonable window for a fix before
any public disclosure.

## Scope notes for self-hosters

- All secrets are supplied via environment variables or the encrypted
  in-database credentials vault — never commit a filled `.env` file.
- File bytes are encrypted per-tenant before reaching blob storage; protect
  `TRILLI_ROOT_KEY` accordingly. Anyone holding both a database dump and the
  root key can unwrap the master key.
- The operator surface (`/api/admin/*`) is closed unless a service token is
  provisioned in the vault.
