package main

// Parallel Do, on when the models section has "- parallel: yes". Ready
// tasks of an epic don't depend on each other, so they go out together as
// one BATCH: the planner sets how many by how it splits the work. Each Do works in its own git worktree at the same BASE and leaves
// its commit there (pdca_wt_commit). Then one MERGE agent cherry-picks the
// commits onto the branch, resolves conflicts and removes the code the tasks
// duplicated, since they couldn't see each other. A task it can't merge goes
// back to an ordinary Do on top of the merged HEAD.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// maxBatch caps how many Do agents run at once; more ready tasks wait for
// the next batch.
const maxBatch = 8

var (
	beadRE  = regexp.MustCompile(`(?m)^Bead:\s*(\S+)\s*$`)
	mergeRE = regexp.MustCompile(`(?m)^PDCA:\s*merge\s*$`)
)

// wtPath is the worktree of a task of a batch. It lies in the repo's
// self-ignored .pdca/: the main tree's status never shows it, and sandboxes
// that let agents write only the workspace (Codex) allow it.
func wtPath(id string) string { return filepath.Join(repoRoot(), ".pdca", "wt", id) }

// printBatch prints the Do dispatches of a batch: all of them for a new
// batch (claiming the tasks), only the unfinished ones on a retry.
func printBatch(d *Dispatch, fresh bool) {
	var ms []Dispatch
	for _, id := range d.Batch {
		t := show(id)
		if !fresh && (t.Meta("pdca_wt_commit") != "" || t.Meta("pdca_blocked") != "") {
			continue
		}
		if fresh && t.Status == "open" {
			bdClaim(id)
		}
		ms = append(ms, Dispatch{Phase: "DO", ID: id, Epic: d.Epic, Base: d.Base, Size: t.Meta("pdca_size"),
			Review: d.Review, Worktree: wtPath(id)})
	}
	how := "all Agent calls in one message"
	if currentHarness() == "codex" {
		how = "spawn_agent for each, then wait_agent for each"
	}
	say(fmt.Sprintf("BATCH %d: start these %d agents at once (%s), wait until every one has replied, then run %s next",
		len(ms), len(ms), how, scriptPath()))
	for i := range ms {
		printDispatch(&ms[i])
	}
}

// cmdWorktree makes the worktree of one Do of the pending batch, at BASE,
// with that Do as its own pending phase, so pdca brief and pdca done run
// inside it as usual. The Do agent runs it, so in Codex the escalation it
// gets for git covers it.
func cmdWorktree(args []string) int {
	_, pos := parseArgs("worktree", args, map[string]bool{})
	if len(pos) != 1 {
		usageFail("worktree")
	}
	b := needPending("BATCH")
	id := pos[0]
	if !slices.Contains(b.Batch, id) {
		fail("%s is not in the pending batch (%s)", id, strings.Join(b.Batch, " "))
	}
	path := wtPath(id)
	if !fileExists(path) {
		if rc, o, e := git("worktree", "add", "--detach", path, b.Base); rc != 0 {
			fail("git worktree add: %s", firstLine(e, o))
		}
	}
	// a rerun keeps what the Do already recorded there
	if !fileExists(filepath.Join(path, ".pdca", "pending.json")) {
		d := Dispatch{Phase: "DO", ID: id, Epic: b.Epic, Base: b.Base, Size: show(id).Meta("pdca_size"), Review: b.Review, Worktree: path}
		js, _ := json.MarshalIndent(d, "", "  ")
		writeStateAt(path, "pending.json", string(js)+"\n")
		h, _ := readFile(statePath("harness"))
		writeStateAt(path, "harness", h)
	}
	say("OK worktree "+path+" at BASE "+short(b.Base), "run every command of the task there: cd "+shellQuote(path)+" && ...")
	return 0
}

// settleBatch verifies that every Do of the batch either committed in its
// worktree or reported a blocker. The commits get a ref, so the worktrees
// can go before the merge: the main tree's test runs must not find them.
func settleBatch(d *Dispatch) (string, []string) {
	var res, open []string
	for _, id := range d.Batch {
		t := show(id)
		switch c := t.Meta("pdca_wt_commit"); {
		case c != "":
			res = append(res, id+"="+short(c))
			if rc, _, _ := git("update-ref", "refs/pdca/"+id, c); rc == 0 {
				removeWorktree(id)
			}
		case t.Meta("pdca_blocked") != "":
			res = append(res, id+"=BLOCKED")
			removeWorktree(id)
		default:
			open = append(open, id)
		}
	}
	if len(open) > 0 {
		return "", []string{"no verified commit from " + strings.Join(open, ", ") + " (its Do ends with pdca done in its worktree)"}
	}
	return "OK batch " + strings.Join(res, " "), nil
}

func removeWorktree(id string) {
	if p := wtPath(id); fileExists(p) {
		git("worktree", "remove", "--force", p)
	}
}

// fastForward merges the only commit of a batch itself when it sits right on
// HEAD. The MERGE dispatch is saved first, so a crash after the merge leaves
// it for pdca next to verify.
func fastForward(d *Dispatch) bool {
	savePending(d)
	if rc, _, _ := git("merge", "--ff-only", "--quiet", d.Commits[d.Batch[0]]); rc != 0 {
		return false
	}
	ok, problems := settleMerge(d, "")
	if len(problems) > 0 {
		fail("merge %s: %s", d.ID, strings.Join(problems, "; "))
	}
	say(ok)
	return true
}

// settleMerge verifies the merge: on top of BASE, one commit per merged task
// (its Bead trailer) and at most one cleanup commit (Bead: <epic>, PDCA:
// merge). The merged commits become the tasks' commits; a task left out goes
// back to Do.
func settleMerge(d *Dispatch, note string) (string, []string) {
	var problems []string
	if rc, _, _ := git("rev-parse", "-q", "--verify", "CHERRY_PICK_HEAD"); rc == 0 {
		problems = append(problems, "a cherry-pick is still in progress (git cherry-pick --continue, or --abort)")
	}
	merged := map[string]string{}
	cleanup := ""
	rc, o, _ := git("rev-list", "--reverse", d.Base+"..HEAD")
	if rc != 0 {
		problems = append(problems, "HEAD doesn't descend from BASE "+short(d.Base))
	}
	for _, c := range strings.Fields(o) {
		msg := gitOK("log", "-1", "--format=%B", c)
		id := ""
		if m := beadRE.FindStringSubmatch(msg); m != nil {
			id = m[1]
		}
		switch {
		case slices.Contains(d.Batch, id) && merged[id] == "":
			merged[id] = c
		case slices.Contains(d.Batch, id):
			problems = append(problems, fmt.Sprintf("%s has two commits (%s, %s); each task keeps exactly one", id, short(merged[id]), short(c)))
		case id == d.Epic && mergeRE.MatchString(msg) && cleanup == "":
			cleanup = c
		default:
			problems = append(problems, fmt.Sprintf("commit %s is neither a task's commit (Bead: <task>) nor the one cleanup commit (Bead: %s, PDCA: merge)",
				short(c), d.Epic))
		}
	}
	if rc == 0 && len(merged) == 0 {
		problems = append(problems, "no commit of the batch is on the branch (cherry-pick them in the order given)")
	}
	problems = append(problems, cleanTree("Merge")...)
	if len(problems) > 0 {
		return "", problems
	}
	var parts, redo []string
	for _, id := range d.Batch {
		c := merged[id]
		if c == "" {
			redo = append(redo, id)
			d.once("redo "+id, func() string {
				bdUpdate(id, nil, []string{"pdca_wt_commit"}, "PDCA-MERGE: its commit could not be merged; Do it again on top of HEAD")
				return ""
			})
		} else {
			parts = append(parts, id+"="+short(c))
			d.once("merged "+id, func() string {
				bdUpdate(id, []string{"pdca_commit=" + c, "pdca_round=0"}, []string{"pdca_wt_commit"}, "")
				if d.Review == "epic" {
					bdClose(id, fmt.Sprintf("PDCA: committed %s (merged); reviewed with the epic", short(c)))
				}
				return ""
			})
		}
		git("update-ref", "-d", "refs/pdca/"+id)
		removeWorktree(id)
	}
	if note = strings.TrimSpace(note); note != "" {
		d.once("note", func() string {
			bdUpdate(d.Epic, nil, nil, fmt.Sprintf("PDCA-MERGE %s: %s", short(headSHA()), note))
			return ""
		})
	}
	ok := fmt.Sprintf("OK merge %s %s cleanup=%s", d.ID, strings.Join(parts, " "), short(cleanup))
	if len(redo) > 0 {
		ok += " redo=" + strings.Join(redo, ",")
	}
	return ok, nil
}

// briefMerge prints what the merge agent reads: the epic design, and each
// task of the batch with its commit, in the order to cherry-pick them.
func briefMerge(d *Dispatch) {
	epicDesign(d.Epic)
	say("=== the batch, in plan order: cherry-pick the commits in this order")
	for _, id := range d.Batch {
		t := show(id)
		c := d.Commits[id]
		say(fmt.Sprintf("--- %s %s commit=%s", t.ID, t.Title, short(c)), strings.TrimSpace(t.Description),
			gitOK("show", "--stat", "--format=", c))
	}
	if h := headSHA(); !sameSHA(h, d.Base) {
		say("=== HEAD", fmt.Sprintf("HEAD %s is not BASE %s; commits on top of BASE:", short(h), short(d.Base)),
			gitOK("log", "--oneline", d.Base+"..HEAD"))
	}
}
