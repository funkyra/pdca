# MERGE phase

The Do agents of a parallel batch each worked in their own worktree from the
same BASE, blind to each other. You put their commits onto the branch, make
them fit together and remove what they duplicated. Check reviews the result
afterwards.

Inputs from the dispatch: `EPIC`, `BASE` (HEAD now) and `TASKS`:
`<task>=<commit>` pairs in plan order.

## 1. Read

`pdca brief`: the epic design (interfaces, conventions, test commands) and,
for each task of the batch, its goal and the files its commit changed.

## 2. Merge

Work in the repo itself. Cherry-pick the commits one at a time, in the given
order: `git cherry-pick <commit>`.

- On a conflict, resolve it so that both tasks' behavior survives as the
  design and their goals describe it, then `git add <paths>` and
  `git cherry-pick --continue --no-edit`. Each commit keeps its message and
  `Bead:` trailer, and each task keeps exactly one commit.
- If a commit can't be merged without redesigning it (the tasks contradict
  each other), run `git cherry-pick --abort`, leave it out and say why in the
  note. pdca sends that task back to Do on top of the merged result.

## 3. Tidy

The tasks couldn't see each other. Look across the batch's changes for:

- duplicates: the same helper, type, constant, parser, fixture or test
  written twice, maybe under different names;
- two names or formats for one concept, and anything that contradicts the
  design;
- leftovers: unused imports, dead code, a stub one task filled and another
  filled again.

Keep one version (the one the design names, else the more complete one),
point every caller and test at it, and delete the rest. Don't add behavior
and don't touch code outside the batch's changes. Then build and run the
full suite: it must pass. If you changed anything, commit it as one commit:

```
refactor(<scope>): unify the code of the parallel tasks

<what was deduplicated or aligned, 1–3 lines>

Bead: <EPIC>
PDCA: merge
```

Stage paths, never `git add -A`, and never stage `.beads/`. Never amend or
rebase anything at or below BASE, and never push. In a sandbox that keeps
`.git` read-only (Codex), git commands that write need escalated
permissions: request them.

## 4. Finish

```bash
pdca done --note "tests '<command>' -> <result>; <conflicts resolved, duplicates removed, commits left out and why>
- Decision: <what> — <why> — <cost if wrong>"
```

Put each judgment call (which duplicate you kept, how you resolved a
conflict) on its own `- Decision:` line of the note. If `pdca` prints
`FAIL`, fix what it names and run `pdca done` again.

## Return

Your final message is the output of your last `pdca done`, verbatim, and
nothing else.

Don't spawn subagents.
