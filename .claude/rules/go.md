---
paths:
  - "**/*.go"
  - "go.mod"
---

# Go conventions

Follow Effective Go and the Go Code Review Comments wiki. On top of those:

## Style

- Wrap errors with context: `fmt.Errorf("load endpoint %s: %w", id, err)`. Use `%w` (not `%v`) when callers may need `errors.Is`/`errors.As`. Don't log and return the same error.
- `context.Context` is the first parameter, named `ctx`. Never store it in a struct.
- Define interfaces where they are consumed, keep them small, and return concrete types.
- Standard library first. A new dependency needs a reason the standard library can't meet; the chosen stack is listed in `docs/design.md`.
- Log with `log/slog`. Pass the logger in; don't use the global default in library code.
- Use the domain terms from `GLOSSARY.md` in identifiers: `Delivery`, `Attempt`, `Endpoint`, never `job`, `try`, `target`.

## Layout

- `cmd/herald-api`, `cmd/herald-dispatcher`, `cmd/herald-admin`: `main` packages only. They wire config and dependencies and call into `internal/`; no business logic.
- `internal/`: everything else, as packages named for what they provide (`signing`, `outbox`), not for what they contain (`utils`, `models`, `helpers`).
- herald-api code never reads the `delivery` schema and herald-dispatcher code never reads the `ingest` schema (ADR 0002).
- Generated code (sqlc, buf) is committed and never edited by hand. Change the source and regenerate.
