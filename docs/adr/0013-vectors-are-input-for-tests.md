# Vectors are input for tests, and aren't required

This ADR supersedes two bullets of [ADR 0007](0007-code-regenerated-from-the-spec.md)'s "Form". The rest of 0007 stands.

- "Test vectors are readable tables, each tagged with the requirement it exercises. The checks read the same file a person reads."
- "CI fails if an implemented MUST has no vector, or a vector names a requirement that doesn't exist. A planned requirement needs no vector, so it can be written down before anyone knows how to test it."

Read plainly, the first makes the vectors the test suite: the checks parse them when they run. The second asks every implemented MUST for a table, whether or not anyone is likely to misread it. Neither is what vectors are for.

The spec's main job is to be what Herald's code, and its tests, are regenerated from. A vector is part of that input: it pins down a requirement where prose alone is easy to misread. In ADR 0007's trial, both implementations built from paragraphs missed one sentence of the retry schedule, and neither noticed. Tests written from the same reading would have missed it too. A vector, written by whoever wrote the requirement, is an independent case that catches that. So:

- **Vectors are input for writing tests, not tests in themselves.** Whoever builds the code turns them into tests however suits the code: a test may read the table directly, or be written from it. Every row of a vector is a case every build must pass, so none is skipped.
- **Tests go beyond the vectors.** The implementer adds cases of their own. The vectors are a minimum, not the whole suite.
- **Vectors aren't required.** A requirement has a vector where a misreading is likely, such as a grammar, a schedule, a limit at its boundaries, or an exception written "Unlike R-n". Being an implemented MUST is not a reason on its own.

The rest of 0007's second bullet stands: CI fails if a vector names a requirement that doesn't exist.

## Considered Options

- **Vectors as the test suite, required for every implemented MUST** (ADR 0007 as written): rejected. A vector earns its place by catching a misreading, and a table for a rule nobody is likely to misread catches none. Tests limited to the vectors would test no more than the spec's examples.

## Consequences

- Kind, in ADR 0007's terms: none. Like 0007, it governs the spec, not the code regenerated from it.
- The spec check stops failing on an implemented MUST with no vector. It still fails on a vector file that names a requirement that doesn't exist, a table in a vector file under no requirement's heading, and the other structural problems [spec/README.md](../../spec/README.md) lists.
- A test written from a vector, rather than reading it, can drift from the vector when it changes. Nothing checks that mechanically.
- A vector can't fail on its own. Where ADR 0007's "Changing the spec" has the spec and its vectors first, failing, what fails is the tests written from them.
- Where ADR 0007, or anything that cites it, says the checks read the vectors or that an implemented MUST needs one, this ADR is what holds.
