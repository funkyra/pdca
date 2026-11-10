package main

// End-to-end tests against real git and bd in temp repos. The test plays the
// phase agents: it writes the commits, reviews and act files they would.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type env struct {
	t    *testing.T
	repo string
}

const testModels = "## PDCA models: claude\n- plan: opus medium\n- do: sonnet medium\n" +
	"- check: opus high\n- act: haiku medium\n- do_L: opus medium\n"

func newEnv(t *testing.T, review string) *env {
	return setupEnv(t, testModels+review, true)
}

// setupEnv makes a repo under a temp home; agentsMd (if any) goes into the
// repo's parent directory, where pdca finds it like a user-level AGENTS.md.
func setupEnv(t *testing.T, agentsMd string, beads bool) *env {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not installed")
	}
	top := t.TempDir()
	repo := filepath.Join(top, "repo")
	script, _ := filepath.Abs("../pdca")
	t.Setenv("PDCA_SCRIPT", script)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(top, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(top, "codex"))
	t.Setenv("PDCA_HOME", top) // links for ~/.agents/skills and ~/.local/bin
	t.Setenv("BEADS_ACTOR", "test")
	if agentsMd != "" {
		writeFile(filepath.Join(top, "AGENTS.md"), agentsMd)
	}
	e := &env{t, repo}
	os.MkdirAll(repo, 0o755)
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	e.sh("git init -q -b main && git config user.name t && git config user.email t@example.invalid && " +
		"printf 'x = 1\\n' > app.py && git add -A && git commit -qm init")
	if beads {
		e.sh("bd init --non-interactive -p tp >/dev/null 2>&1 && git add -A && (git diff --cached --quiet || git commit -qm beads)")
	}
	return e
}

func (e *env) sh(cmd string) string {
	e.t.Helper()
	c := exec.Command("bash", "-c", cmd)
	c.Dir = e.repo
	b, err := c.CombinedOutput()
	if err != nil {
		e.t.Fatalf("%s: %v\n%s", cmd, err, b)
	}
	return strings.TrimSpace(string(b))
}

// run runs a pdca command in-process and checks its exit code and output.
func (e *env) run(code int, args []string, want ...string) string {
	e.t.Helper()
	var buf bytes.Buffer
	out = bufio.NewWriter(&buf)
	got := runMain(args)
	out.Flush()
	s := buf.String()
	if got != code {
		e.t.Fatalf("pdca %s: exit %d, want %d\n%s", strings.Join(args, " "), got, code, s)
	}
	for _, w := range want {
		if !regexp.MustCompile(w).MatchString(s) {
			e.t.Fatalf("pdca %s: output lacks /%s/\n%s", strings.Join(args, " "), w, s)
		}
	}
	return s
}

func (e *env) file(name, text string) string {
	p := filepath.Join(filepath.Dir(e.repo), name)
	writeFile(p, text)
	return p
}

// commit makes a Do commit for task.
func (e *env) commit(file, line, msg string) {
	e.t.Helper()
	e.sh("printf '" + line + "\\n' >> " + file + " && git add " + file + " && git commit -qm '" + msg + "'")
}

func field(s, re string) string {
	m := regexp.MustCompile(re).FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestAgentsFlow(t *testing.T) {
	e := newEnv(t, "- review: task\n")
	e.run(1, []string{"preflight", "--harness", "claude"}, `FAIL no-agents: this project isn't set up for pdca.*init.*pdca once`)
	e.run(0, []string{"install", "--project"}, `OK installed 4 pdca-\* agent file\(s\) in `+regexp.QuoteMeta(e.repo)+`/\.claude/agents`)
	if st := e.sh("git status --porcelain"); st != "" {
		t.Fatalf("project agents dirty the tree: %q", st)
	}
	e.run(0, []string{"preflight", "--harness", "claude"}, `WARN on-protected-branch`)
	e.run(0, []string{"preflight", "--harness", "claude", "--branch", "pdca/t"}, `OK .*branch=pdca/t`,
		`MODELS plan=opus/medium do=sonnet/medium check=opus/high act=haiku/medium do_L=opus/medium check_L=opus/high .* review=task`)

	bad := e.file("bad.md", "# Epic: E\nreq\n## Design\nd\n# Task: A\nSize: M\ngoal\n")
	e.run(1, []string{"plan-apply", bad}, `FAIL plan: epic has no ## Acceptance; task 1 \(A\) has no ## Acceptance; .*nothing was created`)
	if n := e.sh("bd list --all --json | grep -c '\"id\"' || true"); n != "0" {
		t.Fatalf("a refused plan created %s beads", n)
	}

	plan := e.file("plan.md", "# Epic: Feature\nThe request.\n## Design\nUse app.py.\n## Acceptance\n- all good\n"+
		"## Decisions\n- Decision: one — two — three\n"+
		"# Task: First\nSize: M\nGoal one\n## Acceptance\n- a1\n"+
		"# Task: Second\nSize: L\nDeps: 1\nGoal two\n## Acceptance\n- a2\n")
	o := e.run(0, []string{"plan-apply", plan}, `OK plan epic=\S+ tasks=\S+,\S+ decisions=1`,
		`NEXT DO \S+\.1\n`, `AGENT pdca-do model=sonnet effort=medium\n`, `PROMPT TASK: \S+\.1 EPIC: \S+ ROUND: 0 BASE: [0-9a-f]{7} MODE: initial`)
	epic := field(o, `epic=(\S+)`)
	t1, t2 := epic+".1", epic+".2"
	e.run(0, []string{"brief"}, `DISPATCH DO `+t1, `=== epic design\nUse app.py.`, `HEAD is BASE`)

	// Do: no commit, then a commit without the trailer, then a good one
	e.run(1, []string{"done"}, `FAIL do `+t1+`: no commit on top of BASE`)
	e.commit("app.py", "y = 2", "feat: y")
	e.run(1, []string{"done"}, `lacks the trailer line 'Bead: `+t1+`'`)
	e.sh("git commit -q --amend -m 'feat: y' -m 'Bead: " + t1 + "'")
	e.run(0, []string{"done", "--note", "tests ok"}, `OK do `+t1+` commit=[0-9a-f]{7} files=1 \+1 -0 round=0`,
		`NEXT CHECK `+t1, `AGENT pdca-check model=opus effort=high`, `PROMPT TASK: `+t1+` EPIC: `+epic+` ROUND: 0 COMMIT: `)
	// the orchestrator lost the reply: next re-prints the pending phase
	e.run(0, []string{"next"}, `RETRY CHECK `+t1+`: no review recorded`, `AGENT pdca-check`)
	e.run(0, []string{"brief"}, `DISPATCH CHECK `+t1, `number new findings from F1`, `=== diff\n.*\n`, `\+y = 2`)

	contradict := e.file("r0.md", "VERDICT: PASS\nFindings:\n- F1 [Important] app.py:2 — wrong\n")
	e.run(1, []string{"review", contradict}, `verdict PASS contradicts the review: 1 Critical/Important`)
	r1 := e.file("r1.md", "VERDICT: FAIL\nTests: true -> ok\nAcceptance:\n- [met] a1 — yes\nFindings:\n- F1 [Important] app.py:2 — wrong\nOut of scope:\nnone\n")
	e.run(0, []string{"review", r1}, `OK check `+t1+` commit=\S+ verdict=FAIL findings=C0/I1/M0/O0`,
		`NEXT ACT `+t1, `AGENT pdca-act model=haiku effort=medium`, `MAX_ROUNDS: 3`)
	e.run(0, []string{"brief"}, `DISPATCH ACT`, `PDCA-CHECK [0-9a-f]{7} round 0 VERDICT: FAIL`, `round 0 of max 3`, `open tasks that depend on `+t1+`\n- `+t2)

	a1 := e.file("a1.md", "NEXT: FIX\nFix:\n- F1 — make it right\nRejected:\nnone\nNotes:\n- "+t2+": y is now 3\n")
	e.run(0, []string{"act", a1}, `OK act `+t1+` next=FIX round=0`, `NEXT FIX `+t1, `AGENT pdca-do model=sonnet`,
		`ROUND: 1 BASE: \S+ MODE: fix`)
	o = e.run(0, []string{"brief"}, `=== fix exactly these \(Fix of the newest PDCA-ACT\)\n- F1 — make it right\n`,
		`=== the review items they refer to \(newest PDCA-CHECK\)\n- F1 \[Important\] app.py:2 — wrong`)
	if strings.Contains(o, "VERDICT: FAIL") {
		t.Fatalf("a fix brief carries the Fix items, not the whole review:\n%s", o)
	}
	e.sh("sed -i 's/y = 2/y = 3/' app.py && git commit -qam 'fix: y' -m 'Bead: " + t1 + "' -m 'PDCA: fix-1'")
	e.run(0, []string{"done"}, `OK do `+t1+` .*round=1`, `NEXT CHECK `+t1, `ROUND: 1 COMMIT`)
	e.run(0, []string{"brief"}, `the fix brief this commit had to address`, `number new findings from F2`)
	r2 := e.file("r2.md", "VERDICT: PASS\nPrevious findings:\n- F1 ADDRESSED — fixed\nFindings:\n- F2 [Minor] app.py:2 — name\n"+
		"Follow-ups:\n- [task] Rename y — app.py:2 says y\n")
	o = e.run(0, []string{"review", r2}, `OK check `+t1+` .*verdict=PASS findings=C0/I0/M1/O0`, `NEXT DO `+t2, `AGENT pdca-do model=opus effort=medium`)
	if s := e.sh("bd show " + t1 + " --json"); !strings.Contains(s, `"status": "closed"`) {
		t.Fatalf("PASS did not close %s: %s", t1, s)
	}
	if s := e.sh("bd list --status open -l pdca-followup --json"); !strings.Contains(s, "Rename y") {
		t.Fatalf("follow-up not filed: %s", s)
	}
	e.run(0, []string{"brief"}, `PDCA-ACT: y is now 3`) // Act's note reached the dependent task

	// Do reports a blocker; Plan replaces the task
	e.run(0, []string{"done", "--blocked", "contradiction — split it"}, `OK do `+t2+` BLOCKED`, `NEXT REPLAN `+t2,
		`AGENT pdca-plan model=opus effort=medium`, `PROMPT MODE: replan TASK: `+t2)
	e.run(0, []string{"brief"}, `PDCA-DO BLOCKED: contradiction`)
	rp := e.file("rp.md", "# Task: Second, smaller\nSize: S\nGoal three\n## Acceptance\n- a3\n# Epic\n## Decisions\n- Decision: split — smaller — none\n")
	o = e.run(0, []string{"plan-apply", "--replan", rp}, `OK replan `+t2+` superseded by \S+`, `NEXT DO `)
	t3 := field(o, `superseded by (\S+)`)
	e.commit("app.py", "z = 4", "feat: z\n\nBead: "+t3)
	// the last open task: its Check also judges the epic (FINAL), so no separate epic Check runs
	e.run(0, []string{"done"}, `NEXT CHECK `+t3, `PROMPT TASK: `+t3+` .* FINAL: yes`)
	e.run(0, []string{"brief"}, `=== FINAL: the epic's last task`, `=== epic acceptance\n- all good`, `=== commits since the epic base`)
	e.run(1, []string{"review", e.file("r3a.md", "VERDICT: PASS\nFindings:\nnone\n")}, `add 'EPIC VERDICT: PASS\|FAIL'`)
	e.run(1, []string{"review", e.file("r3b.md", "VERDICT: PASS\nEPIC VERDICT: PASS\nIntegration findings:\n- F3 [Important] not wired\n")},
		`EPIC VERDICT PASS contradicts`)
	e.run(0, []string{"review", e.file("r3.md", "VERDICT: PASS\nEPIC VERDICT: FAIL\nTests: true -> ok\nFindings:\nnone\n"+
		"Epic acceptance:\n- [NOT MET] all good — not wired\nIntegration findings:\n- F3 [Important] app.py — not wired\n")},
		`OK check `+t3+` .*verdict=PASS .*epic=FAIL`, `NEXT EPIC_ACT `+epic, `AGENT pdca-act model=haiku`, `PROMPT EPIC_ACT EPIC: `)
	e.run(0, []string{"brief"}, `PDCA-CHECK-EPIC [0-9a-f]{7} round 0 VERDICT: FAIL`, `F3 \[Important\] app.py — not wired`)
	o = e.run(0, []string{"act", e.file("ae.md", "NEXT: FIX\nFix:\n- F3 — wire it\n# Task: Wire it\nSize: S\nGoal wire\n## Acceptance\n- wired\n")},
		`OK epic-act `+epic+` next=FIX round=0`, `NEXT DO `)
	t4 := field(o, `NEXT DO (\S+)`)
	e.commit("app.py", "wired = True", "fix: wire\n\nBead: "+t4)
	e.run(0, []string{"done"}, `NEXT CHECK `+t4, `FINAL: yes`)
	e.run(0, []string{"review", e.file("r4.md", "VERDICT: PASS\nEPIC VERDICT: PASS\nFindings:\nnone\nIntegration findings:\nnone\n")},
		`OK check `+t4+` .*epic=PASS`, `COMPLETE `+epic)
	e.run(0, []string{"report", epic}, `EPIC `+epic+` \[closed\]`, `commits=4`, `FOLLOWUPS \(1\):\n  \S+ - Rename y`,
		`AUTONOMOUS DECISIONS \(2\):`)
	e.run(1, []string{"next"}, `FAIL no PDCA run in progress`)
	for _, sha := range strings.Fields(e.sh("git log --format=%h main..HEAD")) {
		if !strings.Contains(e.sh("bd list --all --json | grep -o '\"id\": *\"[^\"]*\"' | sed 's/.*\"\\(.*\\)\"/\\1/' | xargs -n1 bd comments --json"), sha) {
			t.Errorf("commit %s is named in no review", sha)
		}
	}
}

// Act escalates; the user's answer sends the task to replanning.
func TestEscalate(t *testing.T) {
	e := newEnv(t, "- review: task\n")
	e.run(0, []string{"install", "--project"}, `OK`)
	e.run(0, []string{"preflight", "--harness", "claude", "--branch", "pdca/x"}, `OK`)
	o := e.run(0, []string{"plan-apply", e.file("p.md", "# Epic: Fix\nreq\n## Design\nd\n## Acceptance\n- ok\n# Task: Fix parse\nSize: S\nType: bug\nGoal\n## Acceptance\n- raises\n")},
		`OK plan .* review=task`, `NEXT DO `)
	task := field(o, `NEXT DO (\S+)`)
	epic := field(o, `epic=(\S+)`)
	e.commit("app.py", "fixed = 1", "fix: parse\n\nBead: "+task)
	e.run(0, []string{"done"}, `NEXT CHECK `+task, `PROMPT TASK: `+task+` EPIC: `+epic+` ROUND: 0 COMMIT: \S+ FINAL: yes`)
	// a FAIL needs no epic verdict even on the last task
	e.run(0, []string{"review", e.file("r.md", "VERDICT: FAIL\nFindings:\n- F1 [Critical] app.py:2 — wrong\n")}, `NEXT ACT `+task, `AGENT pdca-act`)
	e.run(1, []string{"act", e.file("a0.md", "NEXT: ESCALATE\n")}, `needs 'Reason:'`)
	e.run(0, []string{"act", e.file("a.md", "NEXT: ESCALATE\nReason: keep or drop the old API? Recommend: keep it.\n")},
		`ESCALATE `+task+`: keep or drop the old API\? Recommend: keep it\.`, `answer `+task)
	e.run(0, []string{"current"}, `EPIC `+epic)
	e.run(1, []string{"answer", epic, "keep it"}, `epic \S+ has no open question`)
	// the answer goes back to Act (cheap), which may still choose REPLAN
	e.run(0, []string{"answer", task, "keep it"}, `OK answer recorded`, `NEXT ACT `+task, `AGENT pdca-act model=haiku effort=medium`)
	e.run(1, []string{"answer", task, "again"}, `has no open question \(state act\)`)
	e.run(0, []string{"brief"}, `PDCA-ANSWER: keep it`, `round 0 of max 3\n`)
	e.run(0, []string{"act", e.file("a2.md", "NEXT: FIX\nFix:\n- F1 — keep the old API\n")}, `OK act `+task+` next=FIX`, `NEXT FIX `+task)
}

// A phase command that fails midway can be rerun: the beads it already
// filed and the comments it already posted are not made twice, and a
// replan that fails before it supersedes the task leaves the plan as it was.
func TestRetries(t *testing.T) {
	e := newEnv(t, "- review: task\n")
	e.run(0, []string{"install", "--project"}, `OK`)
	e.run(0, []string{"preflight", "--harness", "claude", "--branch", "pdca/r"}, `OK`)
	o := e.run(0, []string{"plan-apply", e.file("p.md", "# Epic: E\nreq\n## Design\nd\n## Acceptance\n- ok\n"+
		"# Task: A\nSize: S\nga\n## Acceptance\n- a\n# Task: B\nSize: S\nDeps: 1\ngb\n## Acceptance\n- b\n")}, `NEXT DO `)
	epic := field(o, `epic=(\S+)`)
	t1, t2 := epic+".1", epic+".2"
	e.commit("app.py", "a = 1", "feat: a\n\nBead: "+t1)
	e.run(0, []string{"done"}, `NEXT CHECK `+t1)
	e.run(0, []string{"review", e.file("r.md", "VERDICT: FAIL\nFindings:\n- F1 [Important] app.py:2 — wrong\n")}, `NEXT ACT `+t1)
	count := func(what string) int {
		var list []Issue
		bdJSON(&list, strings.Fields(what)...)
		return len(list)
	}

	// a Notes item naming no bead is refused before anything is written
	act := "NEXT: CLOSE\nRejected:\n- F1 — fine\nFollow-ups:\n- [task] Tidy app.py — later\nNotes:\n- " + t2 + ": a stays\n"
	e.run(1, []string{"act", e.file("a0.md", strings.Replace(act, t2, "tp-nope", 1))}, `Notes: tp-nope is not a bead`)
	if n := count("list --status open -l pdca-followup"); n != 0 {
		t.Fatalf("a refused act filed %d follow-up(s)", n)
	}
	// bd fails after the follow-up is filed: the rerun doesn't file it again
	fail := failBd(e, "comment")
	e.run(1, []string{"act", e.file("a1.md", act)}, `FAIL bd comment`)
	os.Remove(fail)
	e.run(0, []string{"act", e.file("a1.md", act)}, `OK act `+t1+` next=CLOSE`, `NEXT DO `+t2)
	if n := count("list --status open -l pdca-followup"); n != 1 {
		t.Fatalf("follow-ups after the rerun: %d, want 1", n)
	}
	if s := e.sh("bd comments " + t1 + " --json"); strings.Count(s, "PDCA-ACT ") != 1 || !strings.Contains(e.sh("bd show "+t2+" --json"), "PDCA-ACT: a stays") {
		t.Fatalf("comments on %s: %s", t1, s)
	}

	// a replan with a bad Notes id, then one that fails midway: rerunning creates one replacement
	e.run(0, []string{"done", "--blocked", "too big — split it"}, `NEXT REPLAN `+t2)
	rp := "# Task: B1\nSize: S\ngb1\n## Acceptance\n- b1\n# Epic\n## Design\nd2\n# Notes\n- " + t1 + ": see B1\n"
	e.run(1, []string{"plan-apply", "--replan", e.file("rp0.md", strings.Replace(rp, "- "+t1, "- tp-nope", 1))}, `Notes: tp-nope is not a bead`)
	e.run(1, []string{"plan-apply", e.file("new.md", "# Epic: Other\nr\n## Design\nd\n## Acceptance\n- x\n# Task: X\nSize: S\ng\n## Acceptance\n- x\n")},
		`pending phase is REPLAN `+t2)
	fail = failBd(e, "design-file")
	e.run(1, []string{"plan-apply", "--replan", e.file("rp.md", rp)}, `FAIL bd update`)
	os.Remove(fail)
	e.run(0, []string{"plan-apply", "--replan", e.file("rp.md", rp)}, `OK replan `+t2+` superseded by \S+`, `NEXT DO `)
	if n := count("children " + epic); n != 3 {
		t.Fatalf("children after the rerun: %d, want 3 (A, B, one replacement)", n)
	}
	if !strings.Contains(e.sh("bd show "+epic+" --json"), "d2") || !strings.Contains(e.sh("bd show "+t1+" --json"), "PDCA-PLAN: see B1") {
		t.Fatalf("the rerun skipped the design or the note")
	}
}

// failBd makes bd calls whose arguments contain pattern fail until the
// returned marker file is removed.
func failBd(e *env, pattern string) string {
	real, _ := exec.LookPath("bd")
	if strings.Contains(real, "/shim/") {
		real = os.Getenv("PDCA_REAL_BD")
	}
	dir := filepath.Join(filepath.Dir(e.repo), "shim")
	marker := filepath.Join(dir, "fail")
	writeFile(marker, "")
	writeFile(filepath.Join(dir, "bd"), "#!/bin/sh\nif [ -e '"+marker+"' ]; then case \"$*\" in *"+pattern+"*) echo 'boom' >&2; exit 1;; esac; fi\nexec "+real+" \"$@\"\n")
	os.Chmod(filepath.Join(dir, "bd"), 0o755)
	if !strings.HasPrefix(os.Getenv("PATH"), dir+":") {
		e.t.Setenv("PDCA_REAL_BD", real)
		e.t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	}
	return marker
}

// review: epic (the default): Do commits close their tasks, one Check reviews the whole diff.
func TestEpicReview(t *testing.T) {
	e := newEnv(t, "")
	e.run(0, []string{"install", "--project"}, `OK`)
	e.run(0, []string{"preflight", "--harness", "claude", "--branch", "pdca/e"}, `MODELS .* review=epic`)
	plan := e.file("plan.md", "# Epic: Feature\nreq\n## Design\nd\n## Acceptance\n- works\n"+
		"# Task: A\nSize: S\nga\n## Acceptance\n- a ok\n## Decisions\n- Assumption: a is an int — simplest — low\n"+
		"# Task: B\nSize: S\nDeps: 1\ngb\n## Acceptance\n- b ok\n")
	o := e.run(0, []string{"plan-apply", plan}, `OK plan .* decisions=1 review=epic`, `NEXT DO \S+\.1`)
	epic := field(o, `epic=(\S+)`)
	t1, t2 := epic+".1", epic+".2"
	// state lives in the self-ignored .pdca/, so the tree stays clean (and .git is never written)
	if st := e.sh("git status --porcelain"); st != "" || !strings.Contains(e.sh("ls -a .pdca"), "pending.json") {
		t.Fatalf("status %q, .pdca: %s", st, e.sh("ls -a .pdca"))
	}
	e.run(0, []string{"preflight", "--harness", "claude"}, `OPEN `+epic+` branch=pdca/e Feature`)
	e.commit("app.py", "a = 1", "feat: a\n\nBead: "+t1)
	e.run(0, []string{"done"}, `OK do `+t1+` .* closed \(review: epic\)`, `NEXT DO `+t2)
	e.sh("mkdir -p sub")
	e.commit("sub/b.py", "b = 2", "feat: b\n\nBead: "+t2)
	e.run(0, []string{"done"}, `closed \(review: epic\)`, `NEXT EPIC_CHECK `+epic, `AGENT pdca-check model=opus effort=high`,
		`PROMPT EPIC_CHECK EPIC: `+epic+` HEAD: \S+ ROUND: 0 REVIEW: full`)
	// run from a subdirectory, the brief still holds the whole diff
	os.Chdir(filepath.Join(e.repo, "sub"))
	e.run(0, []string{"brief"}, `=== tasks and their acceptance criteria\n--- `+t1+` \[closed\] commit=\S+ A\n- a ok`,
		`number new findings from F1`, `=== diff [0-9a-f]{7}\.\.HEAD`, `\+a = 1`, `\+b = 2`)
	os.Chdir(e.repo)
	e.run(0, []string{"review", e.file("r1.md", "VERDICT: FAIL\nAcceptance:\n- [met] works\nFindings:\n- F1 [Important] app.py:2 — b is wrong\n")},
		`OK epic-check `+epic+` .*verdict=FAIL`, `NEXT EPIC_ACT `+epic)
	o = e.run(0, []string{"act", e.file("a1.md", "NEXT: FIX\nFix:\n- F1 — fix b\n# Task: Fix b\nSize: S\nfix it\n## Acceptance\n- b right\n")},
		`NEXT DO `)
	t3 := field(o, `NEXT DO (\S+)`)
	e.commit("app.py", "c = 3", "fix: b\n\nBead: "+t3)
	e.run(0, []string{"done"}, `closed \(review: epic\)`, `NEXT EPIC_CHECK `+epic, `ROUND: 1 REVIEW: full`)
	o = e.run(0, []string{"brief"}, `the previous review`, `the fix decision`, `number new findings from F2`, `\+c = 3`)
	if strings.Contains(o, "+a = 1") {
		t.Fatalf("round 1 diff should start at the previous review:\n%s", o)
	}
	e.run(0, []string{"review", e.file("r2.md", "VERDICT: PASS\nPrevious findings:\n- F1 ADDRESSED — fixed\nFindings:\nnone\n")},
		`verdict=PASS`, `COMPLETE `+epic)
	for _, id := range []string{t1, t2, t3} {
		c := e.sh("git log -1 --format=%h --grep='Bead: " + id + "'")
		if !strings.Contains(e.sh("bd comments "+id+" --json"), c) {
			t.Errorf("commit %s of %s is named in no review comment on it", c, id)
		}
	}
	e.run(0, []string{"report", epic}, `\[closed\]`, `commits=3`,
		`AUTONOMOUS DECISIONS \(1\):\n  \[`+t1+`\] - Assumption: a is an int`)
}

func TestInstallInit(t *testing.T) {
	e := setupEnv(t, "", false)
	top := filepath.Dir(e.repo)
	e.sh("echo wip > 'my notes.txt'") // the user's own edit: init must not ask to commit it
	// an older install left Claude Code agents user-level: they are removed, the project gets its own
	legacy := filepath.Join(top, "claude", "agents", "pdca-do.md")
	writeFile(legacy, "---\nname: pdca-do\ndescription: old\n---\nold\n")
	o := e.run(1, []string{"init"}, `OK installed 4 pdca-\* agent file\(s\) in .*/codex/agents`,
		`OK removed .*/claude/agents/pdca-do.md`, `OK installed .*/\.local/bin/pdca -> `,
		`OK installed 4 pdca-\* agent file\(s\) in `+regexp.QuoteMeta(e.repo)+`/\.claude/agents`,
		`OK beads initialized`, `FAIL no-models: ask the user`)
	if fileExists(legacy) || strings.Contains(e.sh("git status --porcelain"), ".claude") {
		t.Fatalf("legacy agent kept, or project agents not ignored:\n%s", e.sh("git status --porcelain"))
	}
	if strings.Contains(o, "notes.txt") {
		t.Fatalf("init asks to commit the user's own file:\n%s", o)
	}
	if !strings.Contains(o, top+"/.agents/skills/pdca -> ") {
		t.Fatalf("no Codex skill link:\n%s", o)
	}
	if r, err := filepath.EvalSymlinks(filepath.Join(top, ".local", "bin", "pdca")); err != nil || !strings.HasSuffix(r, "scripts/pdca") {
		t.Fatalf("~/.local/bin/pdca -> %s (%v)", r, err)
	}
	e.run(0, []string{"install"}, `OK already installed`)
	e.run(1, []string{"init", "--models", "plan=opus/medium do=sonnet/medium check=gpt/high act=haiku/medium"}, `model "gpt"`)
	e.run(0, []string{"init", "--models", "plan=opus/medium do=sonnet/medium check=opus/high act=haiku/medium", "--review", "task"},
		`OK beads already set up`, `OK models written to .*/AGENTS.md`, `MODELS plan=opus/medium .* review=task`,
		`COMMIT before /pdca .*git add AGENTS.md`, `OK project ready`)
	// a second init replaces the section in place, keeping the rest of the file
	e.sh("printf '\\n## Other\\nkeep me\\n' >> AGENTS.md")
	e.run(0, []string{"init", "--models", "plan=opus/high do=sonnet/low check=opus/high act=haiku/low"}, `review=epic`)
	md := e.sh("cat AGENTS.md")
	if strings.Count(md, "## PDCA models: claude") != 1 || !strings.Contains(md, "- do: sonnet low") ||
		strings.Contains(md, "review: task") || !strings.Contains(md, "## Other\nkeep me") {
		t.Fatalf("AGENTS.md:\n%s", md)
	}
	e.sh("git add -A && git commit -qm 'chore: set up pdca'")
	e.run(0, []string{"preflight", "--harness", "claude", "--branch", "pdca/x"}, `OK .*branch=pdca/x`, `MODELS plan=opus/high .* review=epic`)
}

// pdca once starts the harness with the agents passed in, so a project that
// isn't set up for pdca gets a single run without agent files.
func TestOnce(t *testing.T) {
	e := setupEnv(t, testModels, true)
	name, argv, env := onceCommand("claude", "add a flag", []string{"--model", "sonnet"})
	var agents map[string]struct {
		Description, Prompt string
		Tools               []string
	}
	if name != "claude" || len(argv) != 6 || argv[1] != "--agents" || json.Unmarshal([]byte(argv[2]), &agents) != nil ||
		argv[3] != "--model" || argv[5] != "/pdca add a flag" || !slices.Contains(env, "PDCA_AGENTS=session") {
		t.Fatalf("claude command: %s %q", name, argv)
	}
	if len(agents) != 4 || !strings.Contains(agents["pdca-do"].Prompt, "# DO phase") ||
		strings.Join(agents["pdca-plan"].Tools, ",") != "Bash,Read,Grep,Glob" {
		t.Fatalf("session agents: %+v", agents)
	}
	// preflight trusts the session agents, and nothing was written into the project
	e.run(1, []string{"preflight", "--harness", "claude"}, `FAIL no-agents`)
	t.Setenv("PDCA_AGENTS", "session")
	e.run(0, []string{"preflight", "--harness", "claude", "--branch", "pdca/o"}, `OK .*branch=pdca/o`)
	if fileExists(filepath.Join(e.repo, ".claude", "agents")) { // .claude/settings.json is bd init's
		t.Fatal("once wrote agents into the project")
	}
	name, argv, _ = onceCommand("codex", "add a flag", nil)
	if name != "codex" || argv[len(argv)-1] != "$pdca add a flag" || !fileExists(filepath.Join(os.Getenv("CODEX_HOME"), "agents", "pdca-do.toml")) {
		t.Fatalf("codex command: %s %q", name, argv)
	}
}

// A bd failure in the middle of plan-apply leaves no half-made plan behind.
func TestPlanRollback(t *testing.T) {
	e := newEnv(t, "")
	e.run(0, []string{"install", "--project"}, `OK`)
	e.run(0, []string{"preflight", "--harness", "claude", "--branch", "pdca/r"}, `OK`)
	real, _ := exec.LookPath("bd")
	shim := filepath.Join(filepath.Dir(e.repo), "shim")
	writeFile(filepath.Join(shim, "bd"), "#!/bin/sh\ncase \"$*\" in *BOOM*) echo 'boom' >&2; exit 1;; esac\nexec "+real+" \"$@\"\n")
	os.Chmod(filepath.Join(shim, "bd"), 0o755)
	t.Setenv("PATH", shim+":"+os.Getenv("PATH"))
	plan := e.file("p.md", "# Epic: E\nreq\n## Design\nd\n## Acceptance\n- ok\n# Task: A\nSize: S\nga\n## Acceptance\n- a\n# Task: BOOM\nSize: S\ngb\n## Acceptance\n- b\n")
	e.run(1, []string{"plan-apply", plan}, `FAIL bd create: boom; deleted the beads this call had created \(\S+, \S+\.1\)`)
	if s := e.sh("bd list --json"); strings.Contains(s, `"id"`) {
		t.Fatalf("rollback left beads: %s", s)
	}
}
