# PDCA

[Русский](README.ru.md) · [Français](README.fr.md) · [Español](README.es.md)

PDCA is a skill for Claude Code and Codex that runs larger coding changes through **Plan → Do → Check → Act**. Give it a request with a clear outcome. It breaks the work into tasks, implements them in separate Git commits, reviews the result against the acceptance criteria, and sends failed reviews back for fixes or replanning.

Use PDCA for changes that benefit from explicit dependencies and an independent review, such as a feature spanning several modules, a migration, or a refactor with compatibility requirements. If you interrupt a run, you can resume it in a new session and inspect what was done and which decisions the agents made along the way.

## Requirements

- A Unix-like shell and Go 1.22 or later. The `scripts/pdca` wrapper builds the CLI on first use and rebuilds it when the Go sources change.
- Git and a target repository with at least one commit, a clean working tree, and Git author settings for commits.
- The Beads CLI (`bd`) on `PATH`. See the [Beads installation guide](https://github.com/gastownhall/beads/blob/main/docs/getting-started/installation.md).
- A Claude Code or Codex session that can launch custom subagents. The `claude` or `codex` executable must be on `PATH` if you use `pdca once`.

## Install and run

### Install the skill

```sh
git clone https://github.com/funkyra/pdca.git
cd pdca
./scripts/pdca install
export PATH="$HOME/.local/bin:$PATH"
```

The installer symlinks the skill into `~/.claude/skills/pdca` and `~/.agents/skills/pdca` and links the wrapper at `~/.local/bin/pdca`. Keep the checkout in place so the symlinks resolve. If `~/.local/bin` is already on your `PATH`, you can skip the `export` line. Otherwise, add it to your shell configuration to use `pdca` in future terminals.

### Set up the repository you want to change

Open a fresh Claude Code or Codex session in the **target repository** and use the commands for that client.

| Action | Claude Code | Codex |
| --- | --- | --- |
| Set up the repository | `/pdca init` | `$pdca init` |
| Start a run | `/pdca <task>` | `$pdca <task>` |
| Resume an unfinished run | `/pdca resume` | `$pdca resume` |

`init` initializes Beads if needed, asks for the model and reasoning effort for each phase if they are missing, and generates project-local agents in `.claude/agents/` for Claude Code or `.codex/agents/` for Codex. Review and commit the setup changes listed in the output, then open a new session to load the agents. The working tree must be clean before starting the first run.

You can also configure the target repository from the terminal. This example uses Claude Code model aliases.

```sh
pdca init --harness claude \
  --models 'plan=opus/high do=sonnet/medium check=sonnet/high act=haiku/medium' \
  --review epic
```

### Give PDCA a task

Here is an example request for a Codex session.

```text
$pdca Add a dry-run mode to CSV import. Print the number of rows to insert or skip, leave stored data untouched, and cover both modes with tests.
```

Preflight checks Beads, the working tree, the agents, and the model configuration. From a protected branch such as `main`, PDCA switches to a `pdca/<task-slug>` branch. From an existing feature branch, it continues on that branch. When the run finishes, the report includes the branch, commit count, follow-up issues, and the decisions and assumptions the agents recorded. The resulting changes stay on the local branch for you to inspect.

### Run once from a terminal

After installing the skill, run one of these commands from the target repository to start a session with the task.

```sh
pdca once --harness claude 'Add a dry-run mode to CSV import'
pdca once --harness codex 'Add a dry-run mode to CSV import'
```

`once` initializes Beads if needed and starts the chosen client with the task. It passes the phase agents into the Claude Code session or writes them to the project's `.codex/agents/` for Codex. The run still requires phase models. Put extra client arguments after `--`.

## How it works

The main session dispatches a fresh agent for each phase. The Go CLI verifies each phase's output and tells the main session which agent to launch next. The Plan and Check instructions use Codebase Memory's graph tools to explore the target codebase.

| Phase | What it does |
| --- | --- |
| **Plan** | Turns the request into a Beads epic with a design, acceptance criteria, and child tasks with dependencies. Records assumptions and design decisions on the epic. |
| **Do** | Implements and tests one task. Each successful implementation or fix round produces one commit with a `Bead: <task-id>` trailer. |
| **Check** | Reviews the diff and tests it against the acceptance criteria. Records a `PASS` or `FAIL` and checks that the assembled result meets the epic's criteria. |
| **Act** | Reviews the findings after a `FAIL`. It can close the work, send a specific fix back to Do, request a replan, file follow-up issues, or ask for a user decision. |

Beads stores the epic, task dependencies, commit references, reviews, and decisions. Git holds the code changes. The Git-ignored `.pdca/` directory holds local state, including the pending dispatch. A resumed run uses that state, Beads, and Git to verify where it left off and pick up the next phase.

## Configuration

PDCA reads the first `AGENTS.md` with a `PDCA models` section for the selected client. It searches from the repository root through its parent directories, then checks `~/.codex/AGENTS.md` by default. `pdca init --models` writes the section to the repository's `AGENTS.md`. If there is no matching section, `pdca models --set` can save the settings locally in `.pdca/`.

```markdown
## PDCA models: claude
- plan: opus high
- do: sonnet medium
- check: sonnet high
- act: haiku medium
- review: epic
- parallel: no
```

For Codex, use `## PDCA models: codex` and the model IDs and reasoning efforts supported by your Codex session. The four phase rows are required. You can override `do_L` and `check_L` for tasks sized `L`, `epic_check` and `epic_act` for epic-wide phases, and `merge` for integration after a parallel batch. `merge` defaults to the `check` settings.

| Setting | Behavior |
| --- | --- |
| `review: epic` (default) | Check reviews the full epic after its tasks are committed. Act can add fix tasks if the review fails. |
| `review: task` | Check reviews each task commit. The last task's Check also verifies the integrated epic. |
| `parallel: no` (default) | Do runs one task at a time. |
| `parallel: yes` | When several tasks are ready, PDCA launches up to eight at once in separate Git worktrees based on the same commit. Task dependencies determine which tasks can share a batch. A Merge agent brings the commits onto the working branch, resolves conflicts, and consolidates duplicated code before Check. |

## Inspect or recover a run

Run these commands from the target repository.

| Command | What it shows or does |
| --- | --- |
| `pdca models --harness codex` | Show the effective models, reasoning efforts, review mode, and configuration source for Codex. Use `claude` for Claude Code. |
| `pdca current` | Find the open epic for the current branch. |
| `pdca status <epic-id>` | Show task states, commit references, and review verdicts. |
| `pdca brief <epic-or-task-id>` | Read the recorded plan or task details, including acceptance criteria. |
| `pdca report <epic-id>` | Show the branch, commit count, follow-ups, and recorded decisions. Run it on the epic's branch for the correct commit count. |
| `pdca next [<epic-id>]` | Verify the pending phase and print the next dispatch. For normal resumption, use `/pdca resume` or `$pdca resume` in the client. |

- `FAIL no-beads` means preflight could not find a Beads workspace. Check that `bd` is on `PATH`, then use the client's `init` invocation.
- `FAIL dirty-tree` lists existing changes. Commit or set them aside before retrying.
- `FAIL agents-stale` prints the command to refresh the agent definitions. Run it and start a fresh session.
- `FAIL no-models` means the phase models are missing. Set them in `AGENTS.md` or use the `models --set` command printed by preflight.
- `STUCK` reports open tasks with unresolved dependencies. `ESCALATE` asks for a decision before work can continue.

## Repository layout and tests

| Path | Contents |
| --- | --- |
| [`SKILL.md`](SKILL.md) | Instructions for the main session that coordinates the run. |
| [`phases/`](phases/) | Instructions for setup, the phase agents, and parallel planning. |
| [`scripts/pdca`](scripts/pdca) | Shell wrapper that builds and launches the CLI. |
| [`scripts/src/`](scripts/src/) | Go CLI, workflow verification, Beads and Git integration, and tests. |
| [`evals/evals.json`](evals/evals.json) | Skill evaluation scenarios and expected outcomes. |

Run the Go test suite from the PDCA checkout.

```sh
cd scripts/src
go test ./...
```

Workflow tests that use Beads are skipped if `bd` is unavailable.

## License

[Mozilla Public License 2.0](LICENSE).
