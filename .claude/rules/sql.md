---
paths:
  - "**/*.sql"
  - "**/sqlc.yaml"
---

# SQL, sqlc and migrations

- Queries live in `.sql` files with sqlc annotations (`-- name: ClaimDueDeliveries :many`). Never build SQL strings in Go for data access (ADR 0005). Two exceptions, each with a comment saying why:
  - DDL on identifiers in test helpers (`CREATE DATABASE` for a test's own database). Identifiers can't be query parameters.
  - Fixed bootstrap statements in the migrate step (`CREATE SCHEMA`, the advisory lock around the migrations): literals with no values in them.
- After changing a query or migration, run `sqlc generate` and commit the generated code in the same change.
- Migrations are goose files, owned by the service whose schema they change (`ingest` or `delivery`), and never touch the other service's schema.
- Migrations are forward-only in practice: never edit a migration that has been merged; add a new one.
- Work-claiming queries use `FOR UPDATE SKIP LOCKED`, and idempotent inserts use `ON CONFLICT DO NOTHING`. Keep these visible in the SQL; they are the delivery guarantees (ADR 0001, 0002).
- Conditions that depend on time (which Deliveries are due, which keys have expired) take the current time as a parameter (`next_attempt_at <= @now`), never `now()`. Tests control the Go clock, but not Postgres's clock.
