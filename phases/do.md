# DO phase

You implement one task as exactly one git commit. That commit is the only
thing that leaves this phase. The reviewer sees its diff and nothing from your
session, so the commit must stand on its own.

Inputs from the dispatch: `TASK`, `EPIC`, `MODE` (initial | fix), `ROUND`,
`BASE` (HEAD at dispatch time).

## 1. Read

- `pdca brief`: the task (goal, files, interface, tests, acceptance criteria,
  notes from earlier phases) and the epic design (interfaces, conventions,
  test commands).
- **Fix mode:** the brief also lists what to fix (the "Fix" items of the
  newest `PDCA-ACT`) and the review items they refer to. Fix exactly those;
  nothing else from the review is yours.
- The brief ends with the state of HEAD. If an earlier attempt of this phase
  already committed on top of BASE, build on that commit and end with exactly
  one commit on top of BASE. If HEAD moved for any other reason, stop: run
  `pdca done --blocked "HEAD moved off BASE"`.
- Read the code you'll touch and its neighbors, and follow existing patterns.

## 2. Implement

- When the task changes behavior, write the test first, watch it fail,
  implement, and watch it pass.
- Stay inside the task. Necessary work outside it goes into the note in
  step 4; Check files it as a follow-up.
- If the task can't be done as written (a contradiction, a missing
  dependency, a wrong interface), don't improvise a different design. Undo
  your edits (`git restore <paths>`, delete files you created), then run
  `pdca done --blocked "<why> — <what change to the plan would fix it>"`.
- Run the task's test command, plus the wider suite or build if it's
  reasonably fast. Never commit red.

## 3. Commit exactly once

Stage only the files you changed, by path. Never `git add -A` / `git add .`,
never stage `.beads/`. Write the message to a temp file outside the repo:

```
<type>(<scope>): <imperative summary>

<what changed and why, 1–3 lines>
<fix mode only: "Fixes: F1, F3">

Bead: <TASK>
PDCA: do            (fix mode: fix-<ROUND>)
```

```bash
git add <path> <path>
git commit -F "$MSGFILE"
```

In a sandbox that keeps `.git` read-only (Codex), `git add` and `git commit`
need escalated permissions: request them rather than giving up.

If you made several commits, squash them:
`git reset --soft <BASE> && git commit -F "$MSGFILE"`. Never amend or rebase
anything at or below `BASE`, and never push. Afterwards `git status
--porcelain` must be empty (`.beads/` aside).

## 4. Finish

```bash
pdca done --note "tests '<command>' -> <N passed>; <work found outside the task, if any>"
```

It verifies the commit, records it on the task, and prints the next dispatch.
If it prints `FAIL`, fix what it names and run it again.

## Return

Your final message is the output of your last `pdca done`, verbatim, and
nothing else.

Don't spawn subagents or reviewers. Check is a separate phase.
