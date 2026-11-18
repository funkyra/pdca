# Parallel planning (`PARALLEL: yes`)

You decide how much runs at once. Every task whose dependencies are done goes
out together with the others ready at that moment (up to 8; the rest wait for
the next batch), each Do in its own git worktree at the same HEAD, blind to
the others' work. So your dependency graph is the parallelism: a wide graph
finishes fast, a chain runs one task at a time. After each batch, a merge
agent puts the commits onto the branch, resolves conflicts and removes the
code the tasks duplicated. Split as far as the work genuinely allows; the
rest of the plan phase still applies.

1. **Foundation first.** What several tasks need (shared types, interfaces,
   helpers, format or schema changes, config, test fixtures) goes into an
   early task they all depend on. Later tasks use it and never write their
   own copy.
2. **Then independent tasks, each owning its files.** List the exact paths
   under **Files**. When two tasks would edit the same file (a registry, a
   router, a command table, a shared test file), put that edit into the
   foundation as a stub or hook they fill, or give the file to one task and
   make the other depend on it.
3. **Contracts in the design.** Every name, signature and format one
   parallel task uses from another is fixed in the epic design, so each can
   code against it without seeing the other.
4. **Wiring last.** Connecting the pieces and the end-to-end tests go into a
   final task that depends on them.
5. **Each task stands alone.** Its tests pass with its dependencies' code
   alone, without its siblings'.
6. **Split for speed, not for its own sake.** A capability you would plan as
   one task may become two parallel ones when each half is substantial
   (roughly 150+ changed lines). Small work stays whole, since every task
   costs a fresh agent. A chain stays a chain: don't invent independence.

Name the batches in the design in one line (`Parallel: 2, 3, 4 after 1`) and
record the splitting choices under `## Decisions`.
