package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	maxTaskRounds = 3 // fix rounds per task
	maxEpicRounds = 2 // integration rounds per epic
)

// taskState is the phase a task waits for, derived only from bead status and
// metadata, so it survives restarts and compaction.
func taskState(t *Issue) string {
	if t.Status == "closed" {
		return "done"
	}
	if t.Meta("pdca_blocked") != "" {
		return "replan"
	}
	c := t.Meta("pdca_commit")
	if c == "" {
		return "do"
	}
	if !sameSHA(t.Meta("pdca_check_sha"), c) {
		return "check"
	}
	if !sameSHA(t.Meta("pdca_act_sha"), c) {
		return "act"
	}
	switch t.Meta("pdca_act") {
	case "FIX":
		return "fix"
	case "REPLAN":
		return "replan"
	case "ESCALATE":
		return "escalate"
	}
	return "act"
}

// Step is what the orchestrator does next.
type Step struct {
	Kind  string // NEXT COMPLETE STUCK ESCALATE
	D     Dispatch
	Text  string
	Claim bool // a DO whose task is still open: claim it at dispatch
}

func computeNext(scope string) Step {
	root := show(scope)
	head := headSHA()
	if root.IssueType != "epic" {
		if root.Parent == "" {
			fail("%s is not a PDCA epic or one of its tasks", scope)
		}
		return computeNext(root.Parent)
	}
	if root.Status == "closed" {
		return Step{Kind: "COMPLETE", D: Dispatch{ID: root.ID}}
	}
	mode := root.Meta("pdca_review")
	if mode == "" {
		mode = "task" // epics planned before review modes existed
	}
	kids := children(root.ID)
	var inflight []*Issue
	for _, k := range kids {
		s := taskState(k)
		if mode == "epic" && s == "check" {
			// committed but left open (interrupted settle): the epic review covers it
			bdClose(k.ID, fmt.Sprintf("PDCA: committed %s; reviewed with the epic", short(k.Meta("pdca_commit"))))
			k.Status = "closed"
			continue
		}
		if (s != "done" && s != "do") || (s == "do" && k.Status == "in_progress") {
			inflight = append(inflight, k)
		}
	}
	if len(inflight) > 0 {
		st := taskStep(inflight[0], root.ID, head)
		st.D.Review = mode
		if st.D.Phase == "CHECK" && mode == "task" {
			st.D.Final = true // the last open task: its Check also checks the epic
			for _, k := range kids {
				if k.ID != inflight[0].ID && k.Status != "closed" {
					st.D.Final = false
				}
			}
		}
		return st
	}
	ready := readyIn(root.ID)
	sort.SliceStable(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority < ready[j].Priority
		}
		return naturalLess(ready[i].ID, ready[j].ID)
	})
	if len(ready) > 0 {
		r := ready[0]
		return Step{Kind: "NEXT", Claim: true,
			D: Dispatch{Phase: "DO", ID: r.ID, Epic: root.ID, Base: head, Size: r.Meta("pdca_size"), Review: mode}}
	}
	var open []string
	for _, k := range kids {
		if k.Status != "closed" {
			open = append(open, k.ID)
		}
	}
	if len(open) > 0 {
		return Step{Kind: "STUCK", Text: "open tasks not ready (blocked outside the epic?): " + strings.Join(open, ", ")}
	}
	d := Dispatch{ID: root.ID, Epic: root.ID, Round: atoi(root.Meta("pdca_epic_round")), Base: head, Review: mode, Full: mode == "epic"}
	switch {
	case !sameSHA(root.Meta("pdca_epic_check_sha"), head):
		d.Phase = "EPIC_CHECK"
	case !sameSHA(root.Meta("pdca_epic_act_sha"), head):
		d.Phase = "EPIC_ACT"
	case root.Meta("pdca_epic_act") == "ESCALATE":
		return Step{Kind: "ESCALATE", D: d, Text: escalateQuestion(root.ID, "PDCA-ACT-EPIC ")}
	default:
		d.Phase = "EPIC_ACT"
	}
	return Step{Kind: "NEXT", D: d}
}

func taskStep(t *Issue, epic, head string) Step {
	s := taskState(t)
	d := Dispatch{ID: t.ID, Epic: epic, Round: atoi(t.Meta("pdca_round")), Base: head,
		Commit: t.Meta("pdca_commit"), Size: t.Meta("pdca_size")}
	switch s {
	case "done":
		return Step{Kind: "COMPLETE", D: Dispatch{ID: t.ID}}
	case "do":
		d.Phase, d.Round, d.Commit = "DO", 0, ""
		return Step{Kind: "NEXT", D: d, Claim: t.Status == "open"}
	case "fix":
		d.Phase, d.Round = "FIX", d.Round+1
	case "check":
		d.Phase = "CHECK"
	case "act":
		d.Phase = "ACT"
	case "replan":
		d.Phase = "REPLAN"
	case "escalate":
		return Step{Kind: "ESCALATE", D: d, Text: escalateQuestion(t.ID, "PDCA-ACT ")}
	}
	return Step{Kind: "NEXT", D: d}
}

func escalateQuestion(id, prefix string) string {
	c := newestComment(comments(id), prefix)
	if r := splitLabeled(c, "reason")["reason"]; r != "" {
		return strings.Join(strings.Fields(r), " ")
	}
	if c == "" {
		return "(no " + strings.TrimSpace(prefix) + " comment found)"
	}
	return strings.Join(strings.Fields(c), " ")
}

func roleOf(d *Dispatch) (role, agent string) {
	switch d.Phase {
	case "DO", "FIX":
		role, agent = "do", "do"
	case "CHECK":
		role, agent = "check", "check"
	case "ACT":
		return "act", "act"
	case "REPLAN":
		return "plan", "plan"
	case "EPIC_CHECK":
		return "epic_check", "check"
	case "EPIC_ACT":
		return "epic_act", "act"
	}
	if d.Size == "L" {
		role += "_L"
	}
	return role, agent
}

func promptOf(d *Dispatch) string {
	epic := d.Epic
	switch d.Phase {
	case "DO":
		return fmt.Sprintf("TASK: %s EPIC: %s ROUND: 0 BASE: %s MODE: initial", d.ID, epic, short(d.Base))
	case "FIX":
		return fmt.Sprintf("TASK: %s EPIC: %s ROUND: %d BASE: %s MODE: fix", d.ID, epic, d.Round, short(d.Base))
	case "CHECK":
		p := fmt.Sprintf("TASK: %s EPIC: %s ROUND: %d COMMIT: %s", d.ID, epic, d.Round, short(d.Commit))
		if d.Final {
			p += " FINAL: yes"
		}
		return p
	case "ACT":
		return fmt.Sprintf("TASK: %s EPIC: %s ROUND: %d COMMIT: %s MAX_ROUNDS: %d", d.ID, epic, d.Round, short(d.Commit), maxTaskRounds)
	case "REPLAN":
		return fmt.Sprintf("MODE: replan TASK: %s EPIC: %s", d.ID, epic)
	case "EPIC_CHECK":
		p := fmt.Sprintf("EPIC_CHECK EPIC: %s HEAD: %s ROUND: %d", d.ID, short(d.Base), d.Round)
		if d.Full {
			p += " REVIEW: full"
		}
		return p
	case "EPIC_ACT":
		return fmt.Sprintf("EPIC_ACT EPIC: %s HEAD: %s ROUND: %d MAX_ROUNDS: %d", d.ID, short(d.Base), d.Round, maxEpicRounds)
	}
	return ""
}

func printDispatch(d *Dispatch) {
	say(fmt.Sprintf("NEXT %s %s", d.Phase, d.ID))
	_, models := loadModels(currentHarness())
	role, agent := roleOf(d)
	m := models[role]
	say(fmt.Sprintf("AGENT pdca-%s model=%s effort=%s", agent, m.Model, m.Effort))
	say("PROMPT " + promptOf(d))
}

func printStep(s Step) {
	p := scriptPath()
	switch s.Kind {
	case "NEXT":
		if s.Claim {
			bdClaim(s.D.ID)
		}
		savePending(&s.D)
		printDispatch(&s.D)
	case "COMPLETE":
		clearPending()
		say("COMPLETE "+s.D.ID, fmt.Sprintf("then run: %s report %s", p, s.D.ID))
	case "STUCK":
		clearPending()
		say("STUCK " + s.Text)
	case "ESCALATE":
		clearPending()
		say(fmt.Sprintf("ESCALATE %s: %s", s.D.ID, s.Text),
			fmt.Sprintf("ask the user, then run: %s answer %s \"<the user's answer>\"", p, s.D.ID))
	}
}

// ---------------------------------------------------------------- verification

func trailerOK(msg, id string) bool {
	return regexp.MustCompile(`(?m)^Bead:\s*` + regexp.QuoteMeta(id) + `\s*$`).MatchString(msg)
}

func cleanTree(who string) []string {
	if dirty := dirtyPaths(); len(dirty) > 0 {
		return []string{fmt.Sprintf("%s left uncommitted changes: %s (restore or remove them)", who, listShort(dirty, 5))}
	}
	return nil
}

func headIs(want, who string) []string {
	if h := headSHA(); !sameSHA(h, want) {
		return []string{fmt.Sprintf("HEAD moved to %s during %s (it must stay at %s; %s doesn't commit)", short(h), who, short(want), who)}
	}
	return nil
}

func settleDo(d *Dispatch, note string) (string, []string) {
	n := commitsSince(d.Base)
	if n == 0 {
		if show(d.ID).Meta("pdca_blocked") != "" { // pdca done --blocked already recorded it
			return fmt.Sprintf("OK do %s BLOCKED", d.ID), nil
		}
		return "", []string{"no commit on top of BASE " + short(d.Base)}
	}
	var problems []string
	head := headSHA()
	if n != 1 {
		problems = append(problems, fmt.Sprintf("%d commits on top of BASE %s (expected exactly 1; squash: git reset --soft %s && git commit -F MSGFILE)",
			n, short(d.Base), short(d.Base)))
	}
	if !trailerOK(gitOK("log", "-1", "--format=%B", head), d.ID) {
		problems = append(problems, fmt.Sprintf("commit message lacks the trailer line 'Bead: %s' (git commit --amend -F MSGFILE)", d.ID))
	}
	files := 0
	for _, f := range strings.Split(gitOK("show", "--name-only", "--format=", head), "\n") {
		if f != "" && !strings.HasPrefix(f, ".beads/") {
			files++
		}
	}
	if files == 0 {
		problems = append(problems, "the commit changes no files outside .beads/")
	}
	added, deleted := 0, 0
	for _, line := range strings.Split(gitOK("show", "--numstat", "--format=", head), "\n") {
		if p := strings.Split(line, "\t"); len(p) >= 3 {
			added += atoi(p[0])
			deleted += atoi(p[1])
		}
	}
	problems = append(problems, cleanTree("Do")...)
	if len(problems) > 0 {
		return "", problems
	}
	if note = strings.TrimSpace(note); note != "" {
		note = fmt.Sprintf("PDCA-DO %s round %d: %s", short(head), d.Round, note)
	}
	d.once("do "+head, func() string {
		bdUpdate(d.ID, []string{"pdca_commit=" + head, "pdca_round=" + itoa(d.Round)}, nil, note)
		return ""
	})
	ok := fmt.Sprintf("OK do %s commit=%s files=%d +%d -%d round=%d", d.ID, short(head), files, added, deleted, d.Round)
	if d.Review == "epic" {
		bdClose(d.ID, fmt.Sprintf("PDCA: committed %s; reviewed with the epic", short(head)))
		ok += " closed (review: epic)"
	}
	return ok, nil
}

func settleBlocked(d *Dispatch, why string) (string, []string) {
	var problems []string
	if n := commitsSince(d.Base); n > 0 {
		problems = append(problems, fmt.Sprintf("%d commit(s) on top of BASE %s: a blocked Do leaves none (git reset --keep %s)", n, short(d.Base), short(d.Base)))
	}
	problems = append(problems, cleanTree("Do")...)
	if len(problems) > 0 {
		return "", problems
	}
	bdUpdate(d.ID, []string{"pdca_blocked=yes"}, nil, "PDCA-DO BLOCKED: "+strings.TrimSpace(why))
	return fmt.Sprintf("OK do %s BLOCKED", d.ID), nil
}

func settleCheck(d *Dispatch) (string, []string) {
	t := show(d.ID)
	if !d.Recorded || !sameSHA(t.Meta("pdca_check_sha"), d.Commit) {
		return "", []string{fmt.Sprintf("no review recorded for %s (run %s review FILE)", short(d.Commit), scriptPath())}
	}
	var problems []string
	v := t.Meta("pdca_verdict")
	if v == "PASS" && t.Status != "closed" {
		problems = append(problems, "verdict PASS but the task is still open")
	}
	epicV := ""
	if d.Final && v == "PASS" {
		e := show(d.Epic)
		epicV = e.Meta("pdca_epic_verdict")
		if !sameSHA(e.Meta("pdca_epic_check_sha"), d.Commit) {
			problems = append(problems, "FINAL: no integration verdict recorded on the epic")
		} else if epicV == "PASS" && e.Status != "closed" {
			problems = append(problems, "epic verdict PASS but the epic is still open")
		}
	}
	problems = append(problems, headIs(d.Commit, "Check")...)
	problems = append(problems, cleanTree("Check")...)
	if len(problems) > 0 {
		return "", problems
	}
	ok := fmt.Sprintf("OK check %s commit=%s verdict=%s findings=%s", d.ID, short(d.Commit), v, t.Meta("pdca_findings"))
	if epicV != "" {
		ok += " epic=" + epicV
	}
	return ok, nil
}

func settleAct(d *Dispatch) (string, []string) {
	t := show(d.ID)
	if !d.Recorded || !sameSHA(t.Meta("pdca_act_sha"), d.Commit) {
		return "", []string{fmt.Sprintf("no decision recorded for %s (run %s act FILE)", short(d.Commit), scriptPath())}
	}
	var problems []string
	act := t.Meta("pdca_act")
	if act == "CLOSE" && t.Status != "closed" {
		problems = append(problems, "decision CLOSE but the task is still open")
	}
	problems = append(problems, headIs(d.Commit, "Act")...)
	problems = append(problems, cleanTree("Act")...)
	if len(problems) > 0 {
		return "", problems
	}
	return fmt.Sprintf("OK act %s next=%s round=%d", d.ID, act, d.Round), nil
}

func settleEpicCheck(d *Dispatch) (string, []string) {
	e := show(d.ID)
	if !d.Recorded || !sameSHA(e.Meta("pdca_epic_check_sha"), d.Base) {
		return "", []string{fmt.Sprintf("no integration review recorded for %s (run %s review FILE)", short(d.Base), scriptPath())}
	}
	var problems []string
	v := e.Meta("pdca_epic_verdict")
	if v == "PASS" && e.Status != "closed" {
		problems = append(problems, "verdict PASS but the epic is still open")
	}
	problems = append(problems, headIs(d.Base, "Check")...)
	problems = append(problems, cleanTree("Check")...)
	if len(problems) > 0 {
		return "", problems
	}
	return fmt.Sprintf("OK epic-check %s head=%s verdict=%s", d.ID, short(d.Base), v), nil
}

func settleEpicAct(d *Dispatch) (string, []string) {
	e := show(d.ID)
	if !d.Recorded || !sameSHA(e.Meta("pdca_epic_act_sha"), d.Base) {
		return "", []string{fmt.Sprintf("no decision recorded for %s (run %s act FILE)", short(d.Base), scriptPath())}
	}
	var problems []string
	act := e.Meta("pdca_epic_act")
	if act == "CLOSE" && e.Status != "closed" {
		problems = append(problems, "decision CLOSE but the epic is still open")
	}
	problems = append(problems, headIs(d.Base, "Act")...)
	problems = append(problems, cleanTree("Act")...)
	if len(problems) > 0 {
		return "", problems
	}
	return fmt.Sprintf("OK epic-act %s next=%s round=%d", d.ID, act, d.Round), nil
}

func settleReplan(d *Dispatch) (string, []string) {
	if t := show(d.ID); t.Status != "closed" {
		return "", []string{fmt.Sprintf("task %s is still open (run %s plan-apply --replan FILE)", d.ID, scriptPath())}
	}
	return fmt.Sprintf("OK replan %s superseded", d.ID), nil
}

// settle verifies the pending phase and records what pdca owns (the Do
// commit). It returns the OK line, or the problems.
func settle(d *Dispatch, note, blocked string) (string, []string) {
	switch d.Phase {
	case "DO", "FIX":
		if blocked != "" {
			return settleBlocked(d, blocked)
		}
		return settleDo(d, note)
	case "CHECK":
		return settleCheck(d)
	case "ACT":
		return settleAct(d)
	case "EPIC_CHECK":
		return settleEpicCheck(d)
	case "EPIC_ACT":
		return settleEpicAct(d)
	case "REPLAN":
		return settleReplan(d)
	}
	return "", []string{"unknown pending phase " + d.Phase}
}

// finish is how every phase command ends: verify, then print the next step.
func finish(d *Dispatch, note, blocked string) int {
	ok, problems := settle(d, note, blocked)
	if len(problems) > 0 {
		d.LastFail = strings.Join(problems, "; ")
		savePending(d)
		say(fmt.Sprintf("FAIL %s %s: %s", strings.ToLower(d.Phase), d.ID, d.LastFail),
			fmt.Sprintf("fix that, then run %s done", scriptPath()))
		return 1
	}
	say(ok)
	printStep(computeNext(d.scope()))
	return 0
}

func needPending(phases ...string) *Dispatch {
	d := loadPending()
	if d == nil {
		fail("no pending dispatch (the orchestrator runs %s next)", scriptPath())
	}
	for _, p := range phases {
		if d.Phase == p {
			return d
		}
	}
	fail("the pending phase is %s %s, not %s", d.Phase, d.ID, strings.Join(phases, "/"))
	return nil
}

// ---------------------------------------------------------------- commands

func cmdNext(args []string) int {
	_, pos := parseArgs("next", args, map[string]bool{})
	if len(pos) > 1 {
		usageFail("next")
	}
	d := loadPending()
	scope := ""
	if d != nil && (len(pos) == 0 || pos[0] == d.ID || pos[0] == d.scope()) {
		ok, problems := settle(d, "", "")
		if len(problems) > 0 {
			d.LastFail = strings.Join(problems, "; ")
			savePending(d)
			say(fmt.Sprintf("RETRY %s %s: %s", d.Phase, d.ID, d.LastFail))
			printDispatch(d)
			return 0
		}
		say(ok)
		scope = d.scope()
	} else if len(pos) == 1 {
		scope = pos[0]
	} else if scope = currentEpic(); scope == "" {
		fail("no PDCA run in progress on branch '%s' (no pending dispatch, no open epic)", currentBranch())
	}
	printStep(computeNext(scope))
	return 0
}

func cmdDone(args []string) int {
	flags, pos := parseArgs("done", args, map[string]bool{"--note": true, "--blocked": true})
	if len(pos) > 0 || (flags["--note"] != "" && flags["--blocked"] != "") {
		usageFail("done")
	}
	d := needPending("DO", "FIX", "CHECK", "ACT", "REPLAN", "EPIC_CHECK", "EPIC_ACT")
	if (flags["--note"] != "" || flags["--blocked"] != "") && d.Phase != "DO" && d.Phase != "FIX" {
		fail("--note and --blocked are for the Do phase (pending: %s)", d.Phase)
	}
	return finish(d, flags["--note"], flags["--blocked"])
}

func fileArg(cmd string, args []string) string {
	_, pos := parseArgs(cmd, args, map[string]bool{})
	if len(pos) != 1 {
		usageFail(cmd)
	}
	text, ok := readFile(pos[0])
	if !ok {
		fail("can't read %s", pos[0])
	}
	return text
}

// fileFollowups creates follow-up beads (outside the epic) from items.
func fileFollowups(items []string, from, sha string) []string {
	var ids []string
	for _, it := range items {
		typ, title, body := parseFollowup(it)
		body += fmt.Sprintf("\n\nFound by PDCA while reviewing %s at %s.", from, short(sha))
		ids = append(ids, createIssue(newIssue{title: title, typ: typ, description: body,
			labels: []string{"pdca-followup"}, deps: []string{"discovered-from:" + from}}))
	}
	return ids
}

// checkNoteIDs fails, before anything is written, when a Notes item names a
// bead that doesn't exist.
func checkNoteIDs(items []string) {
	for _, it := range items {
		if id, _, ok := parseNoteItem(it); ok && !exists(id) {
			fail("Notes: %s is not a bead (items are '- <task-id>: <what changed>'); nothing was recorded", id)
		}
	}
}

// notesOnce appends each "<task-id>: <text>" item to that task's notes.
func notesOnce(d *Dispatch, items []string, prefix string) {
	for i, it := range items {
		if id, note, ok := parseNoteItem(it); ok {
			d.once("note "+itoa(i), func() string { bdNote(id, prefix+note); return "" })
		}
	}
}

func remember(items []string) {
	for _, it := range items {
		bdRemember(strings.Join(strings.Fields(it), " "))
	}
}

func commentBody(header, text, dropLabel string, extra ...string) string {
	var b strings.Builder
	b.WriteString(header + "\n")
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if m := labelRE.FindStringSubmatch(line); m != nil && normalizeLabel(m[1]) == dropLabel {
			continue
		}
		b.WriteString(line + "\n")
	}
	for _, e := range extra {
		if e != "" {
			b.WriteString(e + "\n")
		}
	}
	return b.String()
}

var reviewLabels = []string{"verdict", "epic verdict", "tests", "acceptance", "epic acceptance", "previous findings",
	"findings", "integration findings", "out of scope", "follow-ups", "lessons"}

// passProblems lists what in a review contradicts a PASS verdict.
func passProblems(acceptance, previous, findings string) []string {
	c, i, _, _ := countFindings(findings, "")
	var bad []string
	if c+i > 0 {
		bad = append(bad, fmt.Sprintf("%d Critical/Important finding(s)", c+i))
	}
	if anyHead(acceptance, "NOT MET") {
		bad = append(bad, "a criterion NOT MET")
	}
	if anyHead(previous, "NOT ADDRESSED") {
		bad = append(bad, "a fix item NOT ADDRESSED")
	}
	return bad
}

func cmdReview(args []string) int {
	text := fileArg("review", args)
	d := needPending("CHECK", "EPIC_CHECK")
	if d.Recorded {
		fail("the review is already recorded; run %s done", scriptPath())
	}
	s := splitLabeled(text, reviewLabels...)
	verdict := firstWord(s["verdict"])
	if verdict != "PASS" && verdict != "FAIL" {
		fail("the review needs a first line 'VERDICT: PASS' or 'VERDICT: FAIL'")
	}
	if verdict == "PASS" {
		if bad := passProblems(s["acceptance"], s["previous findings"], s["findings"]); len(bad) > 0 {
			fail("verdict PASS contradicts the review: %s (that is a FAIL)", strings.Join(bad, ", "))
		}
	}
	epicVerdict := ""
	if d.Phase == "CHECK" && d.Final && verdict == "PASS" {
		epicVerdict = firstWord(s["epic verdict"])
		if epicVerdict != "PASS" && epicVerdict != "FAIL" {
			fail("FINAL: this is the epic's last task; add 'EPIC VERDICT: PASS|FAIL' for the integration check")
		}
		if epicVerdict == "PASS" {
			if bad := passProblems(s["epic acceptance"], "", s["integration findings"]); len(bad) > 0 {
				fail("EPIC VERDICT PASS contradicts the review: %s (that is a FAIL)", strings.Join(bad, ", "))
			}
		}
	}
	followups := listItems(s["follow-ups"])
	if verdict == "FAIL" && len(followups) > 0 {
		say("WARN follow-ups are filed on PASS only; Act files them after a FAIL")
		followups = nil
	}
	if d.Phase == "CHECK" {
		recordTaskReview(d, text, s, verdict, epicVerdict, followups)
	} else {
		recordEpicReview(d, text, s, verdict, followups)
	}
	d.Recorded = true
	savePending(d)
	return finish(d, "", "")
}

func filedLine(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return "Follow-ups filed: " + strings.Join(ids, ", ")
}

// followupsOnce files follow-ups once per phase and returns their ids.
func followupsOnce(d *Dispatch, items []string, from, sha string) []string {
	return splitList(d.once("followups", func() string {
		return strings.Join(fileFollowups(items, from, sha), ",")
	}))
}

func commentOnce(d *Dispatch, step, id, text string) {
	d.once(step, func() string { bdComment(id, text); return "" })
}

func rememberOnce(d *Dispatch, items []string) {
	d.once("lessons", func() string { remember(items); return "" })
}

func recordTaskReview(d *Dispatch, text string, s map[string]string, verdict, epicVerdict string, followups []string) {
	c, i, m, o := countFindings(s["findings"], s["out of scope"])
	var ids []string
	if verdict == "PASS" {
		ids = followupsOnce(d, followups, d.ID, d.Commit)
	}
	header := fmt.Sprintf("PDCA-CHECK %s round %d VERDICT: %s", short(d.Commit), d.Round, verdict)
	commentOnce(d, "comment", d.ID, commentBody(header, text, "verdict", filedLine(ids)))
	setMeta(d.ID, "pdca_check_sha="+resolveSHA(d.Commit), "pdca_verdict="+verdict,
		fmt.Sprintf("pdca_findings=C%d/I%d/M%d/O%d", c, i, m, o))
	if verdict != "PASS" {
		return
	}
	rememberOnce(d, listItems(s["lessons"]))
	bdClose(d.ID, fmt.Sprintf("PDCA: check PASS at %s (fix rounds: %d)", short(d.Commit), d.Round))
	if epicVerdict == "" {
		return
	}
	// FINAL: the last task's Check also judged the assembled result
	e := show(d.Epic)
	var b strings.Builder
	fmt.Fprintf(&b, "PDCA-CHECK-EPIC %s round %d VERDICT: %s\n(integration check by the Check of the last task, %s)\n",
		short(d.Commit), atoi(e.Meta("pdca_epic_round")), epicVerdict, d.ID)
	for _, sec := range [][2]string{{"Tests", "tests"}, {"Acceptance", "epic acceptance"}, {"Findings", "integration findings"}} {
		if v := s[sec[1]]; v != "" {
			fmt.Fprintf(&b, "%s:\n%s\n", sec[0], v)
		}
	}
	commentOnce(d, "epic comment", d.Epic, b.String())
	setMeta(d.Epic, "pdca_epic_check_sha="+resolveSHA(d.Commit), "pdca_epic_verdict="+epicVerdict)
	if epicVerdict == "PASS" {
		bdClose(d.Epic, "PDCA complete")
	}
}

func recordEpicReview(d *Dispatch, text string, s map[string]string, verdict string, followups []string) {
	var ids []string
	if verdict == "PASS" {
		ids = followupsOnce(d, followups, d.ID, d.Base)
	}
	header := fmt.Sprintf("PDCA-CHECK-EPIC %s round %d VERDICT: %s", short(d.Base), d.Round, verdict)
	commentOnce(d, "comment", d.ID, commentBody(header, text, "verdict", filedLine(ids)))
	if d.Full {
		// review: epic - this was the only review of these commits; name each on its task
		for _, k := range children(d.ID) {
			c := k.Meta("pdca_commit")
			if c == "" || sameSHA(k.Meta("pdca_check_sha"), c) {
				continue
			}
			commentOnce(d, "comment "+k.ID, k.ID, fmt.Sprintf("PDCA-CHECK-EPIC %s round %d: commit %s reviewed in the epic diff, verdict %s",
				short(d.Base), d.Round, short(c), verdict))
			setMeta(k.ID, "pdca_check_sha="+c, "pdca_verdict="+verdict)
		}
	}
	setMeta(d.ID, "pdca_epic_check_sha="+d.Base, "pdca_epic_verdict="+verdict)
	if verdict == "PASS" {
		rememberOnce(d, listItems(s["lessons"]))
		bdClose(d.ID, "PDCA complete")
	}
}

func resolveSHA(sha string) string {
	rc, o, _ := git("rev-parse", "--verify", "--quiet", sha+"^{commit}")
	if rc != 0 || o == "" {
		return sha
	}
	return strings.TrimSpace(o)
}

var actLabels = []string{"next", "fix", "rejected", "deferred", "follow-ups", "notes", "lessons", "reason"}

func cmdAct(args []string) int {
	text := fileArg("act", args)
	d := needPending("ACT", "EPIC_ACT")
	if d.Recorded {
		fail("the decision is already recorded; run %s done", scriptPath())
	}
	// an epic act file may carry fix tasks as '# Task:' blocks after the act sections
	actText, taskText := text, ""
	if loc := regexp.MustCompile(`(?mi)^#\s+task\b`).FindStringIndex(text); loc != nil {
		actText, taskText = text[:loc[0]], text[loc[0]:]
	}
	s := splitLabeled(actText, actLabels...)
	next := firstWord(s["next"])
	if d.Phase == "ACT" {
		return actTask(d, text, s, next)
	}
	return actEpic(d, actText, taskText, s, next)
}

func actTask(d *Dispatch, text string, s map[string]string, next string) int {
	t := show(d.ID)
	answered := t.Meta("pdca_answered") != ""
	switch next {
	case "CLOSE":
	case "FIX":
		if len(listItems(s["fix"])) == 0 {
			fail("NEXT: FIX needs the findings to fix under 'Fix:'")
		}
		if d.Round >= maxTaskRounds && !answered {
			fail("round %d is the cap (%d): another identical round won't converge; choose CLOSE (file the open items as follow-ups), REPLAN or ESCALATE", d.Round, maxTaskRounds)
		}
	case "REPLAN", "ESCALATE":
		if strings.TrimSpace(s["reason"]) == "" {
			fail("NEXT: %s needs 'Reason:'", next)
		}
	default:
		fail("the act file needs a first line 'NEXT: CLOSE|FIX|REPLAN|ESCALATE'")
	}
	notes := listItems(s["notes"])
	checkNoteIDs(notes)
	ids := followupsOnce(d, listItems(s["follow-ups"]), d.ID, d.Commit)
	commentOnce(d, "comment", d.ID, commentBody(fmt.Sprintf("PDCA-ACT %s round %d NEXT: %s", short(d.Commit), d.Round, next),
		text, "next", filedLine(ids)))
	notesOnce(d, notes, "PDCA-ACT: ")
	rememberOnce(d, listItems(s["lessons"]))
	var unset []string
	if answered {
		unset = []string{"pdca_answered"}
	}
	bdUpdate(d.ID, []string{"pdca_act=" + next, "pdca_act_sha=" + resolveSHA(d.Commit)}, unset, "")
	if next == "CLOSE" {
		bdClose(d.ID, fmt.Sprintf("PDCA: act CLOSE at %s (fix rounds: %d)", short(d.Commit), d.Round))
	}
	d.Recorded = true
	savePending(d)
	return finish(d, "", "")
}

func actEpic(d *Dispatch, actText, taskText string, s map[string]string, next string) int {
	e := show(d.ID)
	answered := e.Meta("pdca_answered") != ""
	var plan *Plan
	switch next {
	case "CLOSE":
	case "FIX":
		if d.Round >= maxEpicRounds && !answered {
			fail("round %d is the epic cap (%d): CLOSE and file the open items as follow-ups, or ESCALATE if the result is unusable without them", d.Round, maxEpicRounds)
		}
		var problems []string
		plan, problems = parsePlan(taskText)
		problems = append(problems, validatePlan(plan, "fix")...)
		if len(problems) > 0 {
			fail("fix tasks: %s", strings.Join(problems, "; "))
		}
		checkDepsExist(plan.Tasks)
	case "ESCALATE":
		if strings.TrimSpace(s["reason"]) == "" {
			fail("NEXT: ESCALATE needs 'Reason:' with the question and your recommendation")
		}
	default:
		fail("the act file needs a first line 'NEXT: CLOSE|FIX|ESCALATE'")
	}
	ids := followupsOnce(d, listItems(s["follow-ups"]), d.ID, d.Base)
	var newTasks []string
	if plan != nil {
		newTasks = splitList(d.once("tasks", func() string {
			var made []string
			atomically(func() { made = createTasks(plan.Tasks, d.ID) })
			return strings.Join(made, ",")
		}))
	}
	extra := []string{filedLine(ids)}
	if len(newTasks) > 0 {
		extra = append(extra, "Fix tasks: "+strings.Join(newTasks, ", "))
	}
	commentOnce(d, "comment", d.ID, commentBody(fmt.Sprintf("PDCA-ACT-EPIC %s round %d NEXT: %s", short(d.Base), d.Round, next),
		actText, "next", extra...))
	rememberOnce(d, listItems(s["lessons"]))
	kv := []string{"pdca_epic_act=" + next, "pdca_epic_act_sha=" + d.Base}
	if next == "FIX" {
		kv = append(kv, "pdca_epic_round="+itoa(d.Round+1))
	}
	var unset []string
	if answered {
		unset = []string{"pdca_answered"}
	}
	bdUpdate(d.ID, kv, unset, "")
	if next == "CLOSE" {
		bdClose(d.ID, "PDCA complete")
	}
	d.Recorded = true
	savePending(d)
	return finish(d, "", "")
}

// createTasks creates plan tasks as children of epic (or standalone when
// epic is empty). Numeric deps name earlier tasks of the same plan.
func createTasks(tasks []PlanTask, epic string) []string {
	var ids []string
	for _, t := range tasks {
		var deps []string
		for _, dep := range t.Deps {
			if m := depTokRE.FindStringSubmatch(dep); m != nil {
				deps = append(deps, ids[atoi(m[1])-1])
			} else {
				deps = append(deps, dep)
			}
		}
		typ := t.Type
		if typ == "" {
			typ = "task"
		}
		notes := ""
		if t.Decisions != "" {
			notes = "## Autonomous decisions\n" + t.Decisions
		}
		id := createIssue(newIssue{title: t.Title, typ: typ, parent: epic, description: t.Description,
			design: t.Design, acceptance: t.Acceptance, notes: notes, deps: deps, meta: map[string]string{"pdca_size": t.Size}})
		ids = append(ids, id)
	}
	return ids
}

func checkDepsExist(tasks []PlanTask) {
	for _, t := range tasks {
		for _, dep := range t.Deps {
			if !depTokRE.MatchString(dep) && !exists(dep) {
				fail("task %q: Deps %s is not a bead", t.Title, dep)
			}
		}
	}
}

func cmdPlanApply(args []string) int {
	flags, pos := parseArgs("plan-apply", args, map[string]bool{"--replan": false})
	_, replan := flags["--replan"]
	if len(pos) != 1 {
		usageFail("plan-apply")
	}
	text, ok := readFile(pos[0])
	if !ok {
		fail("can't read %s", pos[0])
	}
	mode := "new"
	if replan {
		mode = "replan"
	}
	plan, problems := parsePlan(text)
	problems = append(problems, validatePlan(plan, mode)...)
	if len(problems) > 0 {
		fail("plan: %s; fix the file and run plan-apply again (nothing was created)", strings.Join(problems, "; "))
	}
	checkDepsExist(plan.Tasks)
	if replan {
		return planReplan(plan)
	}
	return planNew(plan)
}

func planNew(plan *Plan) int {
	head := headSHA()
	branch := currentBranch()
	if p := loadPending(); p != nil && p.Phase == "REPLAN" {
		if e := tryShow(p.Epic); e != nil && e.Status != "closed" && e.Meta("pdca_branch") == branch {
			fail("the pending phase is REPLAN %s of epic %s: run %s plan-apply --replan FILE (nothing was created)", p.ID, p.Epic, scriptPath())
		}
	}
	e := plan.Epic
	notes := ""
	if e.Decisions != "" {
		notes = "## Autonomous decisions\n" + e.Decisions
	}
	_, _, review := loadConfig(currentHarness())
	var epic string
	var ids []string
	atomically(func() {
		epic = createIssue(newIssue{title: e.Title, typ: "epic", description: e.Description, design: e.Design,
			acceptance: e.Acceptance, notes: notes,
			meta: map[string]string{"pdca_plan": "approved", "pdca_branch": branch, "pdca_base": head, "pdca_review": review}})
		ids = createTasks(plan.Tasks, epic)
	})
	clearPending()
	say(fmt.Sprintf("OK plan epic=%s tasks=%s decisions=%d review=%s", epic, strings.Join(ids, ","), planDecisions(plan), review))
	printStep(computeNext(epic))
	return 0
}

func planReplan(plan *Plan) int {
	d := needPending("REPLAN")
	if _, started := d.Steps["tasks"]; !started && show(d.ID).Status == "closed" {
		return finish(d, "", "") // an earlier plan-apply --replan already superseded it
	}
	checkNoteIDs(plan.Notes)
	// supersede comes last, so a failure anywhere leaves the task as it was
	ids := splitList(d.once("tasks", func() string {
		var made []string
		atomically(func() {
			made = createTasks(plan.Tasks, d.Epic)
			fresh := set(made...)
			for _, dep := range dependents(d.ID) {
				if fresh[dep.ID] {
					continue
				}
				for _, id := range made {
					bdRaw("dep", "add", dep.ID, id)
				}
			}
			bdRaw("supersede", d.ID, "--with", made[0])
		})
		return strings.Join(made, ",")
	}))
	if e := plan.Epic; e != nil {
		if e.Design != "" {
			d.once("design", func() string {
				withTemp(e.Design, func(p string) { bdRaw("update", d.Epic, "--design-file", p) })
				return ""
			})
		}
		if e.Decisions != "" {
			d.once("decisions", func() string { bdUpdate(d.Epic, nil, nil, e.Decisions); return "" })
		}
	}
	notesOnce(d, plan.Notes, "PDCA-PLAN: ")
	say(fmt.Sprintf("OK replan %s superseded by %s", d.ID, strings.Join(ids, ",")))
	printStep(computeNext(d.scope()))
	return 0
}

func cmdAnswer(args []string) int {
	_, pos := parseArgs("answer", args, map[string]bool{})
	if len(pos) != 2 || strings.TrimSpace(pos[1]) == "" {
		usageFail("answer")
	}
	iss := show(pos[0])
	scope := iss.ID
	if iss.IssueType == "epic" {
		if iss.Meta("pdca_epic_act") != "ESCALATE" || iss.Status == "closed" {
			fail("epic %s has no open question (its Act didn't ESCALATE)", iss.ID)
		}
	} else if iss.Parent == "" {
		fail("%s is not a PDCA epic or one of its tasks", iss.ID)
	} else if s := taskState(iss); s != "escalate" {
		fail("task %s has no open question (state %s)", iss.ID, s)
	}
	bdComment(iss.ID, "PDCA-ANSWER: "+strings.TrimSpace(pos[1]))
	// Act decides again with the answer (a FIX may then go past the round cap)
	if iss.IssueType == "epic" {
		bdUpdate(iss.ID, []string{"pdca_answered=yes"}, []string{"pdca_epic_act_sha"}, "")
	} else {
		bdUpdate(iss.ID, []string{"pdca_answered=yes"}, []string{"pdca_act_sha"}, "")
		scope = iss.Parent
	}
	clearPending()
	say("OK answer recorded on " + iss.ID)
	printStep(computeNext(scope))
	return 0
}
