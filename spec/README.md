# Herald's spec

The spec states every decision in Herald that someone at one of its boundaries can notice: what Herald returns and sends for what it is given, what it does in each case, how long it takes, what resources it uses, what debugging data it leaves, and what operators use. Herald's code is derived from it, never the other way round ([ADR 0007](../docs/adr/0007-code-regenerated-from-the-spec.md)). If the spec and another document disagree about behaviour, the spec wins.

The areas are [delivery](delivery.md), the [Tenant API](tenant-api.md), [security](security.md), [observability](observability.md) and [operations](operations.md), and every value they name is in [limits](limits.md).

This file states the spec's conventions. Its examples are in code blocks, which the check doesn't read.

## Files

- `spec/*.md`: requirements, one file per area of Herald, such as `delivery.md` or `tenant-api.md`.
- `spec/limits.md`: the limits table.
- `spec/vectors/*.md`: vector files, for vectors too long to sit under their requirement.

## Requirements

A requirement is one rule, under a heading of its own, written with `#`s: its ID, a middle dot (`·`), and its status. The text says what Herald MUST, SHOULD or MAY do, using a conditional pattern ("When…", "If… then…", "While…") when the rule holds only in some cases. A `Why:` line gives the reason, and links the ADR where one exists.

```markdown
### R-901 · implemented

When an Attempt gets a 2xx response, Herald MUST NOT make another Attempt for that Delivery.

Why: a 2xx is the Endpoint's acknowledgement ([ADR 0001](../docs/adr/0001-at-least-once-delivery.md)).
```

- **One rule each.** An exception is its own requirement, and names the requirement it limits: "Unlike R-901, …".
- **Keywords.** MUST, SHOULD and MAY, in capitals, as in RFC 2119. MUST NOT and SHOULD NOT count as MUST and SHOULD. Write them in capitals only when they carry that meaning.
- **A requirement ends at the next heading.** Its text, vectors and reason come before any other heading.

### IDs

`R-` and a number, from one sequence across the whole spec, whatever the area: a new requirement takes the highest ID in use plus one. An ID is never reused, and a requirement keeps its ID when it moves to another file.

### Status

- `implemented`: the code does this, and a check shows it.
- `planned`: the rule is decided, and the code doesn't do it yet, or nothing checks that it does.
- `withdrawn`: the rule no longer holds (below).

### Withdrawn requirements

A requirement is never deleted. To remove one, change its status to `withdrawn` and add a `Withdrawn:` line with the reason, keeping its text and its vectors as they were. Its vectors are no longer cases a build must pass.

```markdown
### R-902 · withdrawn

Herald MUST make at most 6 Attempts for a Delivery.

Why: …

Withdrawn: replaced by R-903, which retries for 3 days instead of a fixed count.
```

## Vectors

A vector is a Markdown table of cases for one requirement: given these inputs, Herald gives these results. It pins down a requirement where the prose alone is easy to misread ([ADR 0013](../docs/adr/0013-vectors-are-input-for-tests.md)).

- **Input for tests, not tests in themselves.** Whoever builds the code turns a vector into tests however suits the code: a test may read the table directly, or be written from it. Every row is a case every build must pass, so none is skipped.
- **A minimum, not the whole suite.** Tests add cases of their own.
- **Where a misreading is likely.** A requirement has a vector when it is easy to get wrong: a grammar, a schedule, a limit at its boundaries, or an exception written "Unlike R-n". Being an implemented MUST is not a reason on its own, and no requirement needs a vector.

Every row of a table, its header and delimiter rows included, starts and ends with `|`. A table with no rows below its header is not a vector.

A vector sits under the requirement it exercises, after its text:

```markdown
### R-901 · implemented

When an Attempt gets a 2xx response, Herald MUST NOT make another Attempt for that Delivery.

Why: …

| responses     | attempts |
|---------------|----------|
| 200           | 1        |
| 500, 503, 200 | 3        |
```

A vector too long to read beside its requirement, such as a hundred random retry schedules, goes in a file under `spec/vectors/`, under a heading that starts with the requirement's ID. Every table in a vector file sits under such a heading.

```markdown
## R-904: random retry schedules

| failures | earliest next Attempt |
|----------|-----------------------|
| 1        | 5s                    |
```

## Limits

Numbers and limits, including latency and resource limits, are rows of the table in [limits.md](limits.md): a name, a value, and the reason for that value. A requirement names the limit instead of repeating its value: "within `handoff-lag`".

## Grammars

A grammar, such as the Event type or the filter syntax, is written in ABNF ([RFC 5234](https://www.rfc-editor.org/rfc/rfc5234)) in an `abnf` code block, under a heading of its own in the file of its area. Requirements name its rules: "an Event type MUST match `event-type`".

```abnf
event-type = token *("." token)
```

## The check

`make ci` runs the check (`go test ./internal/spec/`). It fails, naming the file and line, when:

- a vector file names an ID that no requirement has, or has a table under no requirement's heading;
- two requirements have the same ID;
- a heading starts with a malformed ID, such as `R-01`;
- a heading in a requirement file starts with an ID but has no status;
- a requirement has no `Why:` line, or is withdrawn with no `Withdrawn:` line.

## Changing the spec

ADR 0007's "Changing the spec" says how: the spec is edited in place, a behaviour change is the spec and its vectors first, failing, then the code, and git history is the spec's log.
