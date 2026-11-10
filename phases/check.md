# CHECK phase

You verify one commit against its task. The review surface is exactly that
commit's diff: you review the change, not the codebase.

Inputs from the dispatch: `TASK`, `EPIC`, `COMMIT`, `ROUND`, and `FINAL: yes`
on the epic's last task (see "Final task"). For `EPIC_CHECK`, skip to "Epic
mode" at the end.

## 1. Gather

`pdca brief` prints everything in one go: the task and its acceptance
criteria, the epic design (interfaces, conventions, test commands), in fix
rounds the `PDCA-ACT` fix brief this commit had to address, the number your
findings start from, and the commit with its diff.

## 2. The scope rule

- A finding must be anchored to a line this commit added, changed or removed:
  `path:line`, with new-file line numbers (old ones for deletions).
- Read anything else you need to understand the change: callers, callees,
  tests, config, history. That is context, not review surface.
- Breakage the commit causes in untouched code **is** in scope, for example
  a signature changed without updating its callers. Anchor it to the changed
  line that causes it.
- Missing work is in scope when an acceptance criterion needs it. Anchor it
  to the criterion (`AC2: ...`).
- Pre-existing problems in code this commit didn't touch are **not
  findings**. List them as `O#` observations; they never affect the verdict.

## 3. Check

1. **Acceptance criteria.** Mark each one met / NOT MET / can't verify, with
   evidence.
2. **Tests.** Run the task's test command and the relevant suite. Check that
   the new tests exercise the change (would they fail without it?). Changed
   behavior without a test is a finding.
3. **Correctness.** Logic, edge cases, error handling, resource handling and
   concurrency in the changed lines.
4. **Contracts.** Names, signatures and formats match the epic design.
5. **Quality.** Readability, duplication, dead code, naming, repo
   conventions, unrequested extras (YAGNI).
6. **Security.** Injection, unsafe input handling, secrets.

**Fix rounds:** mark each finding listed under "Fix" in the fix brief
ADDRESSED or NOT ADDRESSED, with evidence, and review the fix diff for new
breakage. Don't re-review the original commit.

**Severity:** **Critical**: wrong results, data loss, a security hole, broken
build or tests. **Important**: a criterion not met, a bug on a realistic edge
case, changed behavior without a test, a contract mismatch. **Minor**:
naming, style, small clarity improvements.

**Verdict:** FAIL if there is any Critical or Important finding, any
criterion NOT MET, any NOT ADDRESSED fix item, or failing tests. Otherwise
PASS. Aim for accuracy rather than generosity or harshness: if you can't
point to it in the diff, it isn't a finding, and a real bug isn't Minor just
because it's small.

## 4. Record

Write the review to a temp file outside the repo and run `pdca review "$R"`:

```
VERDICT: PASS|FAIL
Tests: <command> -> <result>
Acceptance:
- [met] <criterion> — <evidence>
- [NOT MET] <criterion> — <what is missing>
Previous findings:                      (fix rounds only)
- F1 ADDRESSED — <evidence>
Findings:
- F4 [Important] src/x.py:42 — <problem>. Suggested fix: <...>
Out of scope:
- O1 src/y.py:10 — <pre-existing issue>
Follow-ups:                             (PASS only)
- [bug|task] <title> — <what and where>
```

Write "none" for empty sections. Number findings from the number the brief
gives. On PASS no Act runs, so list as follow-ups every Minor finding, every
real defect among the observations, and work the Do note says is needed
outside the task. `pdca review` posts the review, files the follow-ups,
closes the task on PASS, and prints the next dispatch.

You are read-only: don't edit, stage or commit anything. Tests may generate
files, so leave `git status --porcelain` as you found it. If `pdca` prints
`FAIL`, fix what it names and run `pdca done`.

## Return

Your final message is the output of your last `pdca` command, verbatim, and
nothing else.

Don't spawn subagents.

## Final task (FINAL: yes)

This is the epic's last open task, so no separate integration check follows.
After reviewing the commit, also judge the assembled result: run the full
suite, verify each epic acceptance criterion from the brief by running or
observing it, and check that the pieces of all tasks are wired together. Add
to the review file:

```
EPIC VERDICT: PASS|FAIL
Epic acceptance:
- [met] <criterion> — <evidence>
Integration findings:
- F7 [Important] <where> — <problem> (responsible commit: <sha7>)
```

The epic verdict counts only when the commit itself passes; if it fails, the
fix comes back to this check. On PASS of both, `pdca review` closes the task
and the epic.

## Epic mode (EPIC_CHECK)

Inputs: `EPIC`, `HEAD`, `ROUND`, and `REVIEW: full` when the epic runs in
review-the-epic mode.

**`REVIEW: full`.** No commit of this epic was reviewed on its own: this is
the code review of the whole change and the integration check in one.
`pdca brief` has the epic design and acceptance, every task's acceptance
criteria, and the diff (round 0: the whole epic; later rounds: the fix
commits since the previous review, plus that review and the fix decision).

- Review the diff with sections 2–3 above: the scope rule, every check item,
  severity and verdict. Mark each task's criteria and each epic criterion met
  or NOT MET in `Acceptance:`.
- In later rounds, mark every finding listed under "Fix" in the fix decision
  ADDRESSED or NOT ADDRESSED under `Previous findings:`, and review only the
  fix diff for new breakage.
- Then the integration check below.

**Integration check** (always): every commit was reviewed already unless
`REVIEW: full`, so this is about the assembled result:

- Build and run the full test suite on HEAD.
- Verify each epic acceptance criterion by running or observing it (CLI, API
  call, script).
- Look at the seams between tasks: are the pieces actually wired together
  (registered, imported, called)?
- For each failure, name the commit responsible if you can (`git log -S`, or
  bisect if it's cheap). The fix task will need it.

Write the same review file (`Findings` naming the responsible commit;
`Lessons:` with a genuinely reusable project insight, rarely) and run
`pdca review "$R"`. On PASS it files the follow-ups and closes the epic.
Return its output verbatim.
