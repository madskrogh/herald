---
name: standards-review
description: "Review the changes since a fixed point (commit, branch, tag, or merge-base) along two axes: Standards (does the code follow this repo's documented coding standards?) and Spec (does the code match what the originating issue and the requirements it touches ask for, and is every decision it makes stated in the spec?). Runs both reviews in parallel sub-agents and reports them side by side. Use when the user wants to review a branch, a PR, work-in-progress changes, or asks to \"review since X\"."
---

Two-axis review of the diff between `HEAD` and a fixed point the user supplies:

- **Standards**: does the code conform to this repo's documented coding standards?
- **Spec**: does the code faithfully implement the originating issue and the requirements it touches, and does every decision it makes appear in the spec?

Both axes run as **parallel sub-agents** so they don't pollute each other's context, then this skill aggregates their findings. Start every sub-agent with a fresh context window.

The Spec axis reads two sources. The GitHub issue is what the change is checked against: fetch it with `gh issue view <number> --comments`, because decisions the human made while the work was underway are recorded as comments. The spec (ADR 0007, `docs/adr/0007-code-regenerated-from-the-spec.md`) holds Herald's requirements; the review reads the ones the change touches.

## Process

### 1. Pin the fixed point

Whatever the user said is the fixed point (a commit SHA, branch name, tag, `main`, `HEAD~5`, etc.). If they didn't specify one, ask for it.

Capture the diff command once: `git diff <fixed-point>...HEAD` (three-dot, so the comparison is against the merge-base). Also note the list of commits via `git log <fixed-point>..HEAD --oneline`.

Before going further, confirm the fixed point resolves (`git rev-parse <fixed-point>`) and the diff is non-empty. A bad ref or empty diff should fail here, not inside two parallel sub-agents.

### 2. Identify the issue and the requirements

Find the originating issue, in this order:

1. Issue references in the commit messages or branch name (`#123`, `Closes #45`, `worktree-issue-12`), fetched with `gh issue view <number> --comments`.
2. A path the user passed as an argument.
3. If nothing is found, ask the user which issue the change answers. If they say there isn't one, the Spec sub-agent works from the requirements alone.

Then collect the requirements the change touches, with their text, vectors and reasons:

- the requirement IDs the issue names;
- every requirement the diff adds, changes or withdraws in the spec;
- the requirements covering the behaviour the diff changes, found by searching the spec for the area (an Endpoint failing, a limit, a span).

If there is neither an issue nor any requirement, the **Spec** sub-agent skips and reports "no issue or requirements available".

### 3. Identify the standards sources

Search the repo for every file that documents how code should be written. When `CODING_STANDARDS.md` or `CONTRIBUTING.md` exists, it must be on the list.

On top of whatever the repo documents, the Standards axis always carries the **smell baseline** below: a fixed set of Fowler code smells (_Refactoring_, ch.3) that applies even when a repo documents nothing. Two rules bind it:

- **The repo overrides.** A documented repo standard always wins; where it endorses something the baseline would flag, suppress the smell.
- **Always a judgement call.** Each smell is a labelled heuristic ("possible Feature Envy"), never a hard violation. Like any standard here, skip anything tooling already enforces.

Each smell reads *what it is* → *how to fix*; match it against the diff:

- **Mysterious Name**: a function, variable, or type whose name doesn't reveal what it does or holds. → rename it; if no honest name comes, the design's murky.
- **Duplicated Code**: the same logic shape appears in more than one hunk or file in the change. → extract the shared shape, call it from both.
- **Feature Envy**: a method that reaches into another object's data more than its own. → move the method onto the data it envies.
- **Data Clumps**: the same few fields or params keep travelling together (a type wanting to be born). → bundle them into one type, pass that.
- **Primitive Obsession**: a primitive or string standing in for a domain concept that deserves its own type. → give the concept its own small type.
- **Repeated Switches**: the same `switch`/`if`-cascade on the same type recurs across the change. → replace with polymorphism, or one map both sites share.
- **Shotgun Surgery**: one logical change forces scattered edits across many files in the diff. → gather what changes together into one module.
- **Divergent Change**: one file or module is edited for several unrelated reasons. → split so each module changes for one reason.
- **Speculative Generality**: abstraction, parameters, or hooks added for needs the spec doesn't have. → delete it; inline back until a real need shows.
- **Message Chains**: long `a.b().c().d()` navigation the caller shouldn't depend on. → hide the walk behind one method on the first object.
- **Middle Man**: a class or function that mostly just delegates onward. → cut it, call the real target direct.
- **Refused Bequest**: a subclass or implementer that ignores or overrides most of what it inherits. → drop the inheritance, use composition.

### 4. Spawn both sub-agents in parallel

Issue both sub-agent calls together, in the foreground, and aggregate the reports they return.

**Standards sub-agent prompt** should include:

- The full diff command and commit list.
- The list of standards-source files you found in step 3, **plus the smell baseline from step 3** pasted in full (the sub-agent has no other access to it).
- The brief: "Report, per file/hunk where relevant, (a) every place the diff violates a documented standard: cite the standard (file + the rule); and (b) any baseline smell you spot: name it and quote the hunk. Distinguish hard violations from judgement calls: documented-standard breaches can be hard, but baseline smells are always judgement calls, and a documented repo standard overrides the baseline. Skip anything tooling enforces. Give each finding a severity: High, Medium or Low. Under 400 words."

**Spec sub-agent prompt** should include (spawn it on the session's model: judging whether someone at Herald's boundaries could notice a decision, and whether a value has a reason, needs as much judgement as the Standards review):

- The diff command and commit list.
- The fetched issue with its comments, and the requirements from step 2 with their text, vectors and reasons.
- The brief: "Report: (a) what the issue or a touched requirement asks for that is missing or partial; (b) behaviour in the diff that wasn't asked for (scope creep); (c) requirements that look implemented but where the implementation looks wrong; (d) unstated decisions: a decision in the diff that someone at Herald's boundaries could notice and that no requirement states. Someone is a Tenant, an Endpoint, an operator, or a developer reading telemetry. Look for a new or changed constant, limit, timeout, count, format or span attribute; the behaviour in a case; something the scenario harness expects; a literal in a Go test that pins behaviour; (e) unreasoned values: a value the diff moves into the spec or a data file without a reason. Quote the issue or requirement line for (a)–(c), and the hunk for (d) and (e). Give each finding a severity: High, Medium or Low. Under 400 words."

If there is neither an issue nor any requirement, skip the Spec sub-agent and note this in the final report.

### 5. Aggregate

Present the two reports under `## Standards` and `## Spec` headings, verbatim or lightly cleaned. Do **not** merge or rerank findings, because the two axes are deliberately separate (see _Why two axes_).

End with a one-line summary: total findings per axis, and the worst issue _within each axis_ (if any). Don't pick a single winner across axes: that's the reranking the separation exists to prevent.

## Why two axes

A change can pass one axis and fail the other:

- Code that follows every standard but implements the wrong thing → **Standards pass, Spec fail.**
- Code that does exactly what the issue asked but breaks the project's conventions → **Spec pass, Standards fail.**

Reporting them separately stops one axis from masking the other.
