# Security Policy

## Reporting a vulnerability

Please **do not open a public issue** for security problems.

Report privately through GitHub: **Security tab > Report a vulnerability** on this
repository (private vulnerability reporting). Include what you found, how to reproduce
it, and the affected commit or version.

You can expect an acknowledgement within a few days. Fixes are made on `main`; this
project has no versioned releases yet, so only the latest `main` is supported.

## Scope

In scope: the API, authentication and role checks, file upload and download handling,
and the SQL migrations.

Out of scope: findings that need a leaked `.env`, a compromised host, or the dev-only
database credentials in `docker-compose.yml` (they exist only for local development and
must never be reused anywhere real).

## If you find a secret in this repository

Tell us privately as above. Anything that was ever committed is treated as compromised
and must be rotated, even after it is removed from history.
