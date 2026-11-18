---
name: pdca
description: Builds a large change through a PDCA cycle of phase agents (Plan, Do, Check, Act) with the plan in beads and one commit per task. Run it with /pdca <task> ($pdca in Codex).
argument-hint: <task> | init | resume
disable-model-invocation: true
---

# PDCA development cycle

The task is the text after `/pdca` (Claude Code) or `$pdca` (Codex). It goes
Plan → Do → Check → Act, each phase in a fresh agent. Beads holds the plan
and all state; git commits are the only way code moves between phases. Each
agent ends with a `pdca` command that verifies its phase, records it in beads
and prints the next dispatch.

`pdca` below means `<SKILL_DIR>/scripts/pdca`, where SKILL_DIR is the base
directory shown when this skill loaded. Run it with its full path.

When the task is `init`, follow `<SKILL_DIR>/phases/setup.md` instead of the
cycle.

## Step 0: Preflight

Run `pdca preflight --harness <claude|codex> --branch pdca/<short-slug>`,
naming the harness you run in (Claude Code or Codex) and a slug of the task.
It switches to that branch only from a protected one (main, master, ...), so
work never lands there. For `resume`, leave out `--branch`. Act on the first
word:

- `FAIL no-beads` → stop. Tell the user the project isn't set up for pdca
  and that `/pdca init` (`$pdca init` in Codex) sets it up; it runs
  `bd init`. Don't initialize anything yourself.
- `FAIL dirty-tree` → stop and ask the user what to do with the listed
  changes. Do agents must never sweep up someone else's edits.
- `FAIL no-agents` → stop and relay it: it names the two ways to run pdca
  here (`pdca init` to keep using it, `pdca once` for a single run).
- `FAIL agents-stale` → run the `install` command it prints, tell the user
  to start a new session so the updated agents load, and stop.
- `FAIL no-models` → follow `<SKILL_DIR>/phases/setup.md`, then rerun
  preflight.
- Any other `FAIL` → stop and tell the user.
- `WARN on-protected-branch` (no `--branch` given) → for a new run, rerun
  with it; for `resume`, go on.
- `OK ... branch=<b> base=<sha>` → the `MODELS` line under it gives the
  model and effort of the Plan agent. `OPEN <epic> branch=<b>` lines list
  unfinished runs.

**Resuming.** When the task is `resume` (or asks to continue an unfinished
run), pick the run from the `OPEN` lines (ask the user if several fit),
`git switch <its branch>`, run `pdca next <epic>`, and continue the loop in
Step 1 without a new planner. After a compaction in the middle of a run, just
run `pdca next`.

In a sandbox that keeps `.git` read-only (Codex), commands that write it
(`pdca preflight --branch`, `git switch`, `git commit`) need escalated
permissions: request them.

## The orchestrator only relays

You copy each dispatch from one agent's reply into the next agent call. Your
context is re-read on every turn of the run, so anything you pull in is paid
for again and again:

- Don't read source files, diffs, `bd show` / `bd comments`, or `pdca brief`.
  Phase agents read those themselves.
- The PROMPT line is the whole prompt. Don't add task text, designs or
  findings to it.
- Don't fix code, triage findings or edit beads yourself. Dispatch the phase
  that owns the work.
- A phase counts as done only when `pdca` printed `OK` for it.

## Step 1: Run the agents

Run one agent at a time and wait for it: the repo has a single working tree.
Only a `BATCH` runs several at once: do what its line says.

| | Claude Code | Codex |
|---|---|---|
| dispatch | Agent tool: `subagent_type`, `model` and `effort` from the AGENT line, `prompt` = the text after `PROMPT `; also `run_in_background: false` when the tool has that parameter | `spawn_agent`: `agent_type`, `model` and `reasoning_effort` from the AGENT line, `fork_turns: "none"`, the PROMPT text as the message; then `wait_agent` (`timeout_ms: 600000`) until it reports |

Start with the planner: `pdca-plan` with the `plan` model and effort from the
`MODELS` line, and this prompt:

```
MODE: new
REPO: <root>   BRANCH: <branch>   BASE: <base sha>
REQUEST (verbatim from the user):
<the task text>
```

When the `MODELS` line has `parallel=yes`, add the line `PARALLEL: yes`
after the `REPO` line.

Every agent replies with what `pdca` printed. Act on it:

| The reply has | You |
|---|---|
| `NEXT <phase> <id>`, `AGENT <type> model=<m> effort=<e>`, `PROMPT <text>` | dispatch exactly that |
| `COMPLETE <epic>` | go to Step 2 |
| `ESCALATE <id>: <question>` | ask the user, run `pdca answer <id> "<answer>"`, dispatch what it prints |
| `STUCK ...` | stop and report the blocked tasks |
| `NEEDS_USER: <question>` (planner) | ask the user, then dispatch the planner again with the answer appended to the request |
| anything else (`FAIL`, no NEXT block) | run `pdca next` and dispatch what it prints; if it finds no run (the planner created nothing), dispatch the planner once more with its reply appended to the request |

`pdca next` verifies the last dispatched phase itself. `RETRY <phase> <id>:
<reason>` means that phase didn't finish: dispatch it again as printed. If
the same phase gets `RETRY` twice in a row, stop and report the reason to the
user.

## Step 2: Finish

Run `pdca report <epic>` and relay it to the user in their language: what was
built, the branch and commit count, follow-up beads created, and the full list
of autonomous decisions. Those decisions were made on the user's behalf
without asking, and this list is how they get to review and correct them.

Don't push, merge, or open a PR. Offer that as the next step, using
superpowers:finishing-a-development-branch if it's available.

## When the user gets involved

Only in these cases: preflight failures; planner `NEEDS_USER`; `ESCALATE`;
`STUCK`; `RETRY` twice for the same phase. Everything else (design choices,
ambiguities, review disputes) is decided by the phase agents and recorded in
beads. Don't stop between tasks to ask whether to continue.
