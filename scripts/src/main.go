// Command pdca keeps the state of the PDCA skill and checks every phase.
//
// The orchestrator never reads code, diffs or task text. Each phase agent
// finishes with a pdca command that verifies what the phase left behind (a
// commit, a review, a decision), records it in beads and prints the next
// dispatch. All state lives in beads and git; the pending dispatch is kept in
// .pdca/ at the repo root, so `pdca next` can always recompute the next step.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const usageText = `pdca: state, checks and dispatch for the PDCA skill.
The first word of the output is the outcome: OK FAIL WARN NEXT BATCH RETRY COMPLETE STUCK ESCALATE EPIC NONE.

Orchestrator:
  preflight --harness claude|codex [--branch NAME]  repo, beads, clean tree, agents, models; switch branch
  install [--project [--harness claude|codex]]
                               install skill links and ~/.local/bin/pdca, or refresh project agents
  init [--harness H] [--models "plan=M/E do=M/E check=M/E act=M/E"] [--review epic|task]
                               set up this project to keep using pdca: beads, the models section, the agents
  once [--harness H] TASK [-- HARNESS ARGS]
                               a single run in a project not set up for pdca: starts the harness with the agents
  models [--harness H] [--set "plan=M/E do=M/E check=M/E act=M/E [review=epic|task] [parallel=yes|no]"]
  next [ID]                    verify the pending phase, then print the next dispatch
  answer ID TEXT               record the user's answer to an ESCALATE, print the next dispatch
  current                      open PDCA epic bound to the current branch
  status EPIC                  one line per task
  report EPIC                  final summary for the user

Phase agents:
  brief [ID]                   what the current phase reads: task, design, reviews, diff
  plan-apply [--replan] FILE   create the epic and tasks from a plan file
  done [--note TEXT | --blocked TEXT]   Do: verify the commit or record a blocker
  worktree TASK                Do of a parallel batch: make the task's worktree at BASE
  review FILE                  Check: record the review; PASS files follow-ups and closes
  act FILE                     Act: record the decision
`

var out = bufio.NewWriter(os.Stdout)

func say(lines ...string) {
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
}

type failure struct {
	msg  string
	code int
}

// fail aborts the command with "FAIL <msg>" and exit code 1.
func fail(format string, a ...any) {
	panic(failure{fmt.Sprintf(format, a...), 1})
}

func usageFail(cmd string) {
	panic(failure{"usage: pdca " + cmdUsage[cmd], 2})
}

type command func(args []string) int

var commands map[string]command

var cmdUsage = map[string]string{
	"preflight":  "preflight --harness claude|codex [--branch NAME]",
	"install":    "install [--project [--harness claude|codex]]",
	"init":       `init [--harness claude|codex] [--models "plan=M/E do=M/E check=M/E act=M/E"] [--review epic|task]`,
	"once":       "once [--harness claude|codex] TASK [-- HARNESS ARGS]",
	"models":     `models [--harness claude|codex] [--set "plan=M/E do=M/E check=M/E act=M/E [review=epic|task] [parallel=yes|no]"]`,
	"next":       "next [ID]",
	"answer":     "answer ID TEXT",
	"current":    "current",
	"status":     "status EPIC",
	"report":     "report EPIC",
	"brief":      "brief [ID]",
	"plan-apply": "plan-apply [--replan] FILE",
	"done":       "done [--note TEXT | --blocked TEXT]",
	"worktree":   "worktree TASK",
	"review":     "review FILE",
	"act":        "act FILE",
}

func init() {
	commands = map[string]command{
		"preflight":  cmdPreflight,
		"install":    cmdInstall,
		"init":       cmdInit,
		"once":       cmdOnce,
		"models":     cmdModels,
		"next":       cmdNext,
		"answer":     cmdAnswer,
		"current":    cmdCurrent,
		"status":     cmdStatus,
		"report":     cmdReport,
		"brief":      cmdBrief,
		"plan-apply": cmdPlanApply,
		"done":       cmdDone,
		"review":     cmdReview,
		"act":        cmdAct,
		"worktree":   cmdWorktree,
	}
}

func main() {
	code := runMain(os.Args[1:])
	out.Flush()
	os.Exit(code)
}

func runMain(args []string) (code int) {
	if len(args) == 0 {
		fmt.Fprint(out, usageText)
		return 2
	}
	c, ok := commands[args[0]]
	if !ok {
		fmt.Fprint(out, usageText)
		return 2
	}
	defer func() {
		if r := recover(); r != nil {
			f, ok := r.(failure)
			if !ok {
				panic(r)
			}
			say("FAIL " + f.msg)
			code = f.code
		}
	}()
	return c(args[1:])
}

// parseArgs splits args into flags and positionals. spec maps a flag name to
// whether it takes a value.
func parseArgs(cmd string, args []string, spec map[string]bool) (map[string]string, []string) {
	flags := map[string]string{}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			pos = append(pos, a)
			continue
		}
		name, val, hasVal := strings.Cut(a, "=")
		takes, known := spec[name]
		if !known || (hasVal && !takes) {
			usageFail(cmd)
		}
		if takes && !hasVal {
			if i+1 >= len(args) {
				usageFail(cmd)
			}
			i++
			val = args[i]
		}
		flags[name] = val
	}
	return flags, pos
}
