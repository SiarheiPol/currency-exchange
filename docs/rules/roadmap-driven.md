# Roadmap-driven work

Every iteration of work is tied to a concrete checkbox in [implementation-roadmap.md](../discussions/implementation-roadmap.md). The roadmap is the to-do list, the design intent, and the changelog at once — keeping it in sync is non-negotiable.

## Hard rules

- **No code without a roadmap item.** If the change does not correspond to an existing unchecked item, the work cannot start. Either find the matching item, or update the roadmap first (see below).
- **One checkbox = one commit.** The commit that implements the item also ticks its checkbox in `implementation-roadmap.md`. Reviewer rejects PRs that ship code without flipping the box, or flip a box without the corresponding code.
- **Plan changes are a separate commit.** Roadmap edits never ride along with code edits. See "Changing the plan" below.
- **No silent rescoping.** From `discussions/agent-development.md`: if the task as written cannot be completed, surface it and request clarification. Do not invent a substitute item.

## Granularity

A "roadmap item" is one checkbox line. Some lines are too large for a single commit (e.g. "Worker loop skeleton") — in that case the spec-author breaks it into sub-items in the same iteration's contract, and the roadmap line is split into two boxes via a plan-change commit *before* coding starts.

Rule of thumb: if you cannot write the failing test for an item in 30 minutes of focused work, the item is too big. Split it in the roadmap first.

## Changing the plan

The plan changes whenever new information arrives — a discussion doc lands, an upstream constraint is discovered, an item turns out to be misordered. The flow is:

1. **Spec-author switches to plan-update mode.** Produces a diff: which checkbox is added, removed, moved, or rephrased; which discussion doc gets updated to back it.
2. **User reviews and approves the diff.** Same approval gate as for code.
3. **Commit, docs only.** Message: `roadmap: <what changed>`. No production code in this commit.
4. **Iteration restarts** from the spec-author with the updated roadmap as input.

This produces a clean git history where intent always lands before implementation.

## What goes in the commit message

For a code commit closing a roadmap item:

```
<area>: <what the item delivers>

Closes roadmap item: <Stage N — short item label>

<optional body: non-obvious decisions, links to discussion docs>
```

For a plan-change commit:

```
roadmap: <what changed>

<short body: why the change is needed; link to discussion doc if applicable>
```

## What this rule replaces

Without it, agents drift — they pick up adjacent work, "improve while passing through", or invent items that never get reviewed. The roadmap stops being a plan and becomes a graveyard of partially-done ideas. Tying every commit to one checkbox keeps the document and the code in lockstep.
