# Setting pdca up

## `/pdca init`

Run `pdca init --harness <claude|codex>`, adding `--models "plan=<m>/<e>
do=<m>/<e> check=<m>/<e> act=<m>/<e>"` and `--review epic|task` if the user
gave them (`parallel=yes` goes inside `--models`). On `FAIL no-models`, ask
the user for the models (below), then run it again with their answers. Relay
what it printed, including the files it asks to commit and the new session it
asks for, and stop.

## `FAIL no-models` from preflight

Ask the user for the models (below), or to write them into AGENTS.md
themselves. Save an answer with the `models --set` command preflight printed,
then rerun preflight.

## Models

Ask for a model and reasoning effort for each phase agent (plan, do, check,
act), for the review mode, and whether Do may run tasks in parallel. Nothing
inherits the session model.

| Phase | Agent | Leaves behind |
|---|---|---|
| Plan | `pdca-plan`, one per epic | epic (design, autonomous decisions) + child tasks |
| Do | `pdca-do`, one per task and fix round | exactly one commit with a `Bead: <id>` trailer |
| Check | `pdca-check`, one for the epic or one per commit (see `review`) | review comment; on PASS follow-ups, the closed task or epic |
| Act | `pdca-act`, one per FAIL | close / fix brief / replan / follow-ups |
| Merge | `pdca-merge`, after each parallel batch (see `parallel`) | the batch's commits on the branch, plus at most one cleanup commit |

`pdca` reads the models from the nearest AGENTS.md that has a section for the
harness (the repo root, its parent directories, then `~/.codex/AGENTS.md`),
otherwise from the answer saved by `pdca models --set` in the repo's `.pdca/`
(which git ignores). `pdca init --models` writes the section into the
project's AGENTS.md:

```markdown
## PDCA models: claude
- plan: opus high
- do: sonnet medium
- check: sonnet high
- act: haiku medium
```

`## PDCA models: codex` holds the Codex models. plan, do, check and act are
required. The optional rows `do_L` and `check_L` (tasks planned as size L),
`epic_check`, `epic_act` and `merge` (on the `check` model by default)
default to their base row. The user owns these choices: they edit the
section, or you save their answer with `pdca models --set`.

- `- review: epic|task` picks how Check runs. `epic` (the default): tasks are
  only committed, and one Check reviews the whole diff of the epic at the
  end; fixes come back as new tasks. `task`: every commit gets its own Check
  and, after a FAIL, its own Act; the Check of the last task also checks the
  assembled epic.
- `- parallel: yes` lets Do run independent tasks at once, each in its own
  git worktree. The planner decides how many by how it splits the work and
  sets dependencies. After each batch the merge agent puts the commits onto
  the branch, resolves conflicts and removes duplicated code. Without the
  row, tasks run one at a time.
