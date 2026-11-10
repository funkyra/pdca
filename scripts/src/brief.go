package main

import (
	"fmt"
	"strings"
)

// wholeRepo limits a diff to the repository without beads' files. Pathspecs
// are relative to the current directory, so these are anchored at the top:
// a phase agent may run pdca from a subdirectory.
var wholeRepo = []string{"--", ":/", ":(top,exclude).beads"}

func gitDiff(args ...string) string { return gitOK(append(args, wholeRepo...)...) }

func section(title, text string) {
	if text = strings.TrimSpace(text); text != "" {
		say("=== "+title, text)
	}
}

func taskHeader(t *Issue) {
	say(fmt.Sprintf("TASK %s [%s] size=%s %s", t.ID, t.Status, orDash(t.Meta("pdca_size")), t.Title))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func taskSections(t *Issue) {
	section("description", t.Description)
	section("design", t.Design)
	section("acceptance", t.AcceptanceCriteria)
	section("notes", t.Notes)
}

func epicDesign(epic string) {
	if epic == "" {
		return
	}
	e := show(epic)
	say(fmt.Sprintf("EPIC %s %s", e.ID, e.Title))
	section("epic design", e.Design)
}

func taskList(epic string) {
	say("=== tasks")
	for _, k := range children(epic) {
		say(fmt.Sprintf("- %s [%s] commit=%s %s", k.ID, k.Status, short(k.Meta("pdca_commit")), k.Title))
	}
}

func dependentList(id string) {
	deps := dependents(id)
	if len(deps) == 0 {
		return
	}
	say("=== open tasks that depend on " + id)
	for _, d := range deps {
		say(fmt.Sprintf("- %s %s", d.ID, d.Title))
	}
}

func nextFinding(cs []Comment, prefix string) int {
	n := 0
	for _, c := range cs {
		if !strings.HasPrefix(strings.TrimSpace(c.Text), prefix) {
			continue
		}
		for _, m := range findingNumRE.FindAllStringSubmatch(c.Text, -1) {
			n = max(n, atoi(m[1]))
		}
	}
	return n + 1
}

// cmdBrief prints what a phase agent reads. Without an ID it briefs the
// pending dispatch, phase by phase; with one it prints that task or epic.
func cmdBrief(args []string) int {
	_, pos := parseArgs("brief", args, map[string]bool{})
	switch len(pos) {
	case 1:
		briefID(pos[0])
		return 0
	case 0:
	default:
		usageFail("brief")
	}
	d := needPending("DO", "FIX", "CHECK", "ACT", "REPLAN", "EPIC_CHECK", "EPIC_ACT")
	head := fmt.Sprintf("DISPATCH %s %s epic=%s round=%d", d.Phase, d.ID, d.Epic, d.Round)
	if d.Phase == "CHECK" || d.Phase == "ACT" {
		head += " commit=" + short(d.Commit)
	} else {
		head += " base=" + short(d.Base)
	}
	say(head)
	section("an earlier attempt of this phase failed verification", d.LastFail)
	switch d.Phase {
	case "DO", "FIX":
		briefDo(d)
	case "CHECK":
		briefCheck(d)
	case "ACT":
		briefAct(d)
	case "REPLAN":
		briefReplan(d)
	case "EPIC_CHECK":
		briefEpicCheck(d)
	case "EPIC_ACT":
		briefEpicAct(d)
	}
	return 0
}

func briefDo(d *Dispatch) {
	t := show(d.ID)
	cs := comments(d.ID)
	taskHeader(t)
	taskSections(t)
	epicDesign(d.Epic)
	if d.Phase == "FIX" {
		fixBrief(cs)
	}
	section("the user's answer", newestComment(cs, "PDCA-ANSWER:"))
	h := headSHA()
	if sameSHA(h, d.Base) {
		say("=== HEAD", "HEAD is BASE "+short(d.Base)+": commit exactly once on top of it")
	} else {
		say("=== HEAD", fmt.Sprintf("HEAD %s is not BASE %s; commits on top of BASE:", short(h), short(d.Base)),
			gitOK("log", "--oneline", d.Base+"..HEAD"))
	}
}

// fixBrief prints what a fix round changes: the Fix items of the newest
// PDCA-ACT and the review items they rest on, not the whole review, which
// also lists findings Act rejected or deferred.
func fixBrief(cs []Comment) {
	act, review := newestComment(cs, "PDCA-ACT "), newestComment(cs, "PDCA-CHECK ")
	fix := listItems(splitLabeled(act, actLabels...)["fix"])
	if len(fix) == 0 {
		section("the review (newest PDCA-CHECK)", review)
		section("your fix brief (newest PDCA-ACT): fix exactly what it lists under Fix", act)
		return
	}
	section("fix exactly these (Fix of the newest PDCA-ACT)", "- "+strings.Join(fix, "\n- "))
	cited := set(findingNumRE.FindAllString(strings.Join(fix, "\n"), -1)...)
	s := splitLabeled(review, reviewLabels...)
	var refs []string
	for _, sec := range []string{"findings", "integration findings", "acceptance", "previous findings"} {
		for _, it := range listItems(s[sec]) {
			h := strings.ToUpper(itemHead(it))
			if n := findingNumRE.FindString(h); (n != "" && cited[n]) || strings.Contains(h, "NOT MET") || strings.Contains(h, "NOT ADDRESSED") {
				refs = append(refs, "- "+it)
			}
		}
	}
	section("the review items they refer to (newest PDCA-CHECK)", strings.Join(refs, "\n"))
}

func briefCheck(d *Dispatch) {
	t := show(d.ID)
	cs := comments(d.ID)
	taskHeader(t)
	taskSections(t)
	epicDesign(d.Epic)
	if d.Round > 0 {
		section("the fix brief this commit had to address (newest PDCA-ACT)", newestComment(cs, "PDCA-ACT "))
	}
	say("=== numbering", fmt.Sprintf("number new findings from F%d", nextFinding(cs, "PDCA-CHECK ")))
	if d.Final {
		e := show(d.Epic)
		say("=== FINAL: the epic's last task", "Also check the assembled result (check.md, Final task) and add EPIC VERDICT.")
		section("epic acceptance", e.AcceptanceCriteria)
		if base := e.Meta("pdca_base"); base != "" {
			say("=== commits since the epic base", gitOK("log", "--oneline", base+"..HEAD"))
		}
	}
	say("=== commit", gitOK("show", "--stat", "--format=commit %H%n%n%B", d.Commit))
	say("=== diff", gitDiff("show", "--format=", d.Commit))
}

func briefAct(d *Dispatch) {
	t := show(d.ID)
	cs := comments(d.ID)
	taskHeader(t)
	section("description", t.Description)
	section("acceptance", t.AcceptanceCriteria)
	epicDesign(d.Epic)
	section("the review to act on (newest PDCA-CHECK)", newestComment(cs, "PDCA-CHECK "))
	if d.Round > 0 {
		section("the previous fix brief (newest PDCA-ACT)", newestComment(cs, "PDCA-ACT "))
	}
	section("the user's answer", newestComment(cs, "PDCA-ANSWER:"))
	cap := fmt.Sprintf("round %d of max %d", d.Round, maxTaskRounds)
	if d.Round >= maxTaskRounds && t.Meta("pdca_answered") == "" {
		cap += ": at the cap, FIX is not allowed"
	}
	say("=== rounds", cap)
	say("=== commit", gitOK("show", "--stat", "--format=%h %s", d.Commit))
	dependentList(d.ID)
}

func briefReplan(d *Dispatch) {
	t := show(d.ID)
	cs := comments(d.ID)
	taskHeader(t)
	taskSections(t)
	epicDesign(d.Epic)
	section("why (newest PDCA-ACT)", newestComment(cs, "PDCA-ACT "))
	section("the user's answer", newestComment(cs, "PDCA-ANSWER:"))
	if d.Epic != "" {
		taskList(d.Epic)
	}
	dependentList(d.ID)
}

func briefEpic(e *Issue) {
	say(fmt.Sprintf("EPIC %s [%s] base=%s %s", e.ID, e.Status, short(e.Meta("pdca_base")), e.Title))
	section("description", e.Description)
	section("design", e.Design)
	section("acceptance", e.AcceptanceCriteria)
	taskList(e.ID)
}

func briefEpicCheck(d *Dispatch) {
	e := show(d.ID)
	base := e.Meta("pdca_base")
	if !d.Full {
		briefEpic(e)
		if base != "" {
			say("=== commits since base", gitOK("log", "--oneline", base+"..HEAD"))
			say("=== changed files", gitDiff("diff", "--stat", base+"..HEAD"))
		}
		return
	}
	// review: epic - the only code review: every task's criteria and the diff
	cs := comments(d.ID)
	say(fmt.Sprintf("EPIC %s [%s] base=%s %s", e.ID, e.Status, short(base), e.Title))
	section("description", e.Description)
	section("design", e.Design)
	section("acceptance", e.AcceptanceCriteria)
	say("=== tasks and their acceptance criteria")
	for _, k := range children(e.ID) {
		say(fmt.Sprintf("--- %s [%s] commit=%s %s", k.ID, k.Status, short(k.Meta("pdca_commit")), k.Title), strings.TrimSpace(k.AcceptanceCriteria))
		if n := strings.TrimSpace(k.Notes); n != "" {
			say("notes: " + n)
		}
	}
	from := base
	if prev := e.Meta("pdca_epic_check_sha"); d.Round > 0 && prev != "" {
		from = prev
		section("the previous review (newest PDCA-CHECK-EPIC)", newestComment(cs, "PDCA-CHECK-EPIC "))
		section("the fix decision (newest PDCA-ACT-EPIC)", newestComment(cs, "PDCA-ACT-EPIC "))
	}
	say("=== numbering", fmt.Sprintf("number new findings from F%d", nextFinding(cs, "PDCA-CHECK-EPIC ")))
	if from == "" {
		return
	}
	say("=== commits "+short(from)+"..HEAD", gitOK("log", "--format=%h %s", from+"..HEAD"))
	say("=== diff "+short(from)+"..HEAD", gitDiff("diff", from+"..HEAD"))
}

func briefEpicAct(d *Dispatch) {
	e := show(d.ID)
	cs := comments(d.ID)
	briefEpic(e)
	section("the integration review to act on (newest PDCA-CHECK-EPIC)", newestComment(cs, "PDCA-CHECK-EPIC "))
	section("the user's answer", newestComment(cs, "PDCA-ANSWER:"))
	cap := fmt.Sprintf("round %d of max %d", d.Round, maxEpicRounds)
	if d.Round >= maxEpicRounds && e.Meta("pdca_answered") == "" {
		cap += ": at the cap, FIX is not allowed"
	}
	say("=== rounds", cap)
}

// briefID prints a task or an epic with its newest PDCA comments.
func briefID(id string) {
	iss := show(id)
	cs := comments(id)
	if iss.IssueType == "epic" {
		briefEpic(iss)
		section("newest PDCA-CHECK-EPIC comment", newestComment(cs, "PDCA-CHECK-EPIC "))
		section("newest PDCA-ACT-EPIC comment", newestComment(cs, "PDCA-ACT-EPIC "))
		return
	}
	taskHeader(iss)
	taskSections(iss)
	epicDesign(iss.Parent)
	section("newest PDCA-CHECK comment", newestComment(cs, "PDCA-CHECK "))
	section("newest PDCA-ACT comment", newestComment(cs, "PDCA-ACT "))
	section("the user's answer", newestComment(cs, "PDCA-ANSWER:"))
}
