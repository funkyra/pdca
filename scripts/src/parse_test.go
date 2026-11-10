package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePlan(t *testing.T) {
	text := "preamble is ignored\n" +
		"# Epic: Budgets\nThe request.\n\n## Design\nApproach A.\n## Components\nstays in design\n```\n# Task: inside a fence\n## Acceptance\n```\n" +
		"## Acceptance\n- suite passes\n## Decisions\n- Decision: a — b — c\n- Assumption: d — e — f\n" +
		"# Task: First\nSize: m\nDeps: none\n\n**Goal** do it\n## Acceptance\n- works\n" +
		"# Task: Second\nSize: S\nType: bug\nDeps: 1, inv-9\nGoal two\n## Acceptance\n- also\n" +
		"# Notes\n- inv-3: renamed X to Y\n"
	p, problems := parsePlan(text)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if p.Epic == nil || p.Epic.Title != "Budgets" || p.Epic.Description != "The request." {
		t.Fatalf("epic: %+v", p.Epic)
	}
	if !strings.Contains(p.Epic.Design, "## Components") || !strings.Contains(p.Epic.Design, "# Task: inside a fence") {
		t.Fatalf("design lost content: %q", p.Epic.Design)
	}
	if p.Epic.Acceptance != "- suite passes" || countDecisions(p.Epic.Decisions) != 2 {
		t.Fatalf("acceptance/decisions: %q %q", p.Epic.Acceptance, p.Epic.Decisions)
	}
	if len(p.Tasks) != 2 {
		t.Fatalf("tasks: %d", len(p.Tasks))
	}
	t1, t2 := p.Tasks[0], p.Tasks[1]
	if t1.Title != "First" || t1.Size != "M" || len(t1.Deps) != 0 || t1.Description != "**Goal** do it" || t1.Acceptance != "- works" {
		t.Fatalf("task 1: %+v", t1)
	}
	if t2.Type != "bug" || strings.Join(t2.Deps, ",") != "1,inv-9" || t2.Description != "Goal two" {
		t.Fatalf("task 2: %+v", t2)
	}
	if len(p.Notes) != 1 || p.Notes[0] != "inv-3: renamed X to Y" {
		t.Fatalf("notes: %q", p.Notes)
	}
	if v := validatePlan(p, "new"); len(v) > 0 {
		t.Fatal(v)
	}
}

func TestValidatePlan(t *testing.T) {
	p, _ := parsePlan("# Epic: E\nd\n# Task: A\nDeps: 2\nno size\n# Task: B\nSize: XL\n")
	got := strings.Join(validatePlan(p, "new"), "; ")
	for _, want := range []string{"epic has no ## Design", "epic has no ## Acceptance", "task 1 (A) has no ## Acceptance",
		"task 1 (A) needs 'Size: S|M|L'", "Deps 2 must name an earlier task", "task 2 (B) has no description"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	long, _ := parsePlan("# Task: A\nSize: S\n" + strings.Repeat("x", maxTaskText+1) + "\n## Acceptance\n- a\n")
	if got := strings.Join(validatePlan(long, "replan"), "; "); !strings.Contains(got, "cut prose") {
		t.Errorf("long task not refused: %q", got)
	}
}

func TestSplitLabeled(t *testing.T) {
	text := "VERDICT: FAIL\nTests: make test -> 2 failed\nAcceptance:\n- [NOT MET] AC1 — missing\n" +
		"Findings:\n- F3 [Important] a.py:4 — bug\n  Suggested fix: guard it\n- F4 [Minor] b.py:1 — name\n" +
		"Out of scope:\nnone\n**Follow-ups:**\n- [bug] Crash on empty — in c.py:9\n- none\n"
	s := splitLabeled(text, reviewLabels...)
	if firstWord(s["verdict"]) != "FAIL" || s["tests"] != "make test -> 2 failed" {
		t.Fatalf("%q", s)
	}
	c, i, m, o := countFindings(s["findings"], s["out of scope"])
	if c != 0 || i != 1 || m != 1 || o != 0 {
		t.Fatalf("counts %d %d %d %d", c, i, m, o)
	}
	if items := listItems(s["findings"]); len(items) != 2 || !strings.Contains(items[0], "Suggested fix") {
		t.Fatalf("items %q", items)
	}
	fu := listItems(s["follow-ups"])
	if len(fu) != 1 {
		t.Fatalf("follow-ups %q", fu)
	}
	typ, title, body := parseFollowup(fu[0])
	if typ != "bug" || title != "Crash on empty" || body != "in c.py:9" {
		t.Fatalf("followup %q %q %q", typ, title, body)
	}
}

func TestModelsSection(t *testing.T) {
	text := "# Codex Instructions\n## PDCA models: codex\n- plan: gpt-5 high\n## PDCA models: claude\n- plan: opus high\n" +
		"- do: sonnet medium\n* check = opus xhigh\n- act: haiku low\n- do_L: opus medium\n## Other\n- plan: nope x\n"
	r := sectionRows(text, "claude")
	if r == nil {
		t.Fatal("no section")
	}
	m := validateModels(r, "claude", "test")
	if m["plan"] != (ModelSpec{"opus", "high"}) || m["do_L"] != (ModelSpec{"opus", "medium"}) ||
		m["check_L"] != (ModelSpec{"opus", "xhigh"}) || m["epic_act"] != (ModelSpec{"haiku", "low"}) {
		t.Fatalf("%v", m)
	}
	if sectionRows("nothing here", "claude") != nil {
		t.Fatal("found a section in text without one")
	}
	bad := sectionRows("## PDCA models: claude\n- plan: gpt-5 high\n- do: sonnet loud\n", "claude")
	func() {
		defer func() {
			f, _ := recover().(failure)
			for _, want := range []string{"check missing", `model "gpt-5"`, `effort "loud"`} {
				if !strings.Contains(f.msg, want) {
					t.Errorf("missing %q in %q", want, f.msg)
				}
			}
		}()
		validateModels(bad, "claude", "test")
	}()
	// efforts are checked against what each harness accepts
	minimal := sectionRows("## PDCA models: x\n- plan: opus minimal\n- do: sonnet low\n- check: opus max\n- act: haiku low\n", "x")
	if _, f := tryValidate(minimal, "codex"); f != "" {
		t.Errorf("codex takes minimal: %s", f)
	}
	if _, f := tryValidate(minimal, "claude"); !strings.Contains(f, `plan: effort "minimal" (claude takes low, medium, high, xhigh, max)`) {
		t.Errorf("claude must refuse minimal: %q", f)
	}
}

func tryValidate(r *rows, harness string) (m Models, msg string) {
	defer func() {
		if f, ok := recover().(failure); ok {
			msg = f.msg
		}
	}()
	return validateModels(r, harness, "test"), ""
}

func TestWriteModelsSection(t *testing.T) {
	p := filepath.Join(t.TempDir(), "AGENTS.md")
	writeFile(p, "# Project\nintro\n\n## PDCA models: claude\n- plan: opus high\n- review: task\n\n"+
		"The user's note about models.\n- do: sonnet low\n\n## Other\nkeep me\n")
	writeModelsSection(p, "claude", parseModelSpec("plan=opus/low do=sonnet/medium check=opus/high act=haiku/low"))
	text, _ := readFile(p)
	want := "# Project\nintro\n\n## PDCA models: claude\n- plan: opus low\n- do: sonnet medium\n- check: opus high\n- act: haiku low\n\n" +
		"The user's note about models.\n\n## Other\nkeep me\n"
	if text != want {
		t.Fatalf("got:\n%s\nwant:\n%s", text, want)
	}
}

func TestDecodeJSON(t *testing.T) {
	var v []map[string]any
	if !decodeJSON("[warn] slow disk\nnote {x}\n[{\"id\": \"a-1\"}]", &v) || len(v) != 1 || v[0]["id"] != "a-1" {
		t.Fatalf("warning lines before the JSON: %v", v)
	}
	if decodeJSON("no json here", &v) {
		t.Fatal("decoded text without JSON")
	}
}

func TestPassProblems(t *testing.T) {
	if bad := passProblems("- [met] AC1 — it was NOT MET before the fix\n", "- F1 ADDRESSED — the NOT ADDRESSED case is gone\n",
		"- F2 [Minor] a.py:1 — mentions [important] data\n"); len(bad) > 0 {
		t.Fatalf("explanations count as status: %v", bad)
	}
	if bad := passProblems("- [NOT MET] AC2 — missing\n", "- F1 NOT ADDRESSED — still there\n", "- F3 a.py:2 — [Important] bug\n"); len(bad) != 3 {
		t.Fatalf("missed a status: %v", bad)
	}
}

func TestStatusPaths(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(dir)
	for _, c := range []string{"git init -q", "printf x > old.txt", "git add old.txt",
		"git -c user.name=t -c user.email=t@e.invalid commit -qm i", "git mv old.txt new.txt", "printf y > 'a b.txt'"} {
		if out, err := exec.Command("sh", "-c", c).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v %s", c, err, out)
		}
	}
	if got := strings.Join(statusPaths(), "|"); got != "new.txt|a b.txt" {
		t.Fatalf("statusPaths: %q", got)
	}
}
