package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

var harnesses = []string{"claude", "codex"}

// scriptPath is the wrapper users and agents run; skillDir is its skill.
func scriptPath() string {
	if p := os.Getenv("PDCA_SCRIPT"); p != "" {
		return p
	}
	exe, err := os.Executable()
	if err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		// built as <skill>/scripts/.bin/pdca
		return filepath.Join(filepath.Dir(filepath.Dir(exe)), "pdca")
	}
	return "pdca"
}

func skillDir() string { return filepath.Dir(filepath.Dir(scriptPath())) }

// ---------------------------------------------------------------- state

// statePath is per-repo pdca state in <root>/.pdca/, which ignores itself
// (.gitignore "*"), so it never dirties the tree. Not in .git/: sandboxes such
// as Codex's workspace-write keep .git read-only.
func statePath(name string) string {
	root := repoRoot()
	if root == "" {
		fail("not inside a git repository")
	}
	return filepath.Join(root, ".pdca", name)
}

func writeState(name, text string) { writeStateAt(repoRoot(), name, text) }

// writeStateAt writes pdca state into the .pdca/ of the worktree at root.
func writeStateAt(root, name, text string) {
	p := filepath.Join(root, ".pdca", name)
	if ign := filepath.Join(filepath.Dir(p), ".gitignore"); !fileExists(ign) {
		writeFile(ign, "# pdca state, never committed\n*\n")
	}
	writeFile(p, text)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Dispatch is the phase the orchestrator was told to run last.
type Dispatch struct {
	Phase    string `json:"phase"` // DO FIX CHECK ACT REPLAN EPIC_CHECK EPIC_ACT BATCH MERGE
	ID       string `json:"id"`    // task, or epic for epic phases
	Epic     string `json:"epic,omitempty"`
	Round    int    `json:"round"`
	Base     string `json:"base"`             // HEAD when dispatched
	Commit   string `json:"commit,omitempty"` // the commit under review (CHECK, ACT)
	Size     string `json:"size,omitempty"`
	Review   string `json:"review,omitempty"`   // the epic's review mode: epic | task
	Final    bool   `json:"final,omitempty"`    // CHECK of the epic's last open task (review: task)
	Full     bool   `json:"full,omitempty"`     // EPIC_CHECK that is the only code review (review: epic)
	Recorded bool   `json:"recorded,omitempty"` // review / act file applied
	LastFail string `json:"last_fail,omitempty"`
	// parallel Do ("- parallel: yes"): the tasks of a BATCH or MERGE, in
	// plan order, the commit each made in its worktree (MERGE), and the
	// worktree of one Do of a batch
	Batch    []string          `json:"batch,omitempty"`
	Commits  map[string]string `json:"commits,omitempty"`
	Worktree string            `json:"worktree,omitempty"`
	// Steps are the writes this phase already made that a rerun must not
	// repeat (follow-ups filed, comments posted), with what they produced.
	Steps map[string]string `json:"steps,omitempty"`
}

// once runs a write that must happen only once per phase. When a phase
// command fails midway and the agent runs it again, the writes it already
// made are skipped, so nothing is filed or posted twice.
func (d *Dispatch) once(step string, f func() string) string {
	if v, ok := d.Steps[step]; ok {
		return v
	}
	v := f()
	if d.Steps == nil {
		d.Steps = map[string]string{}
	}
	d.Steps[step] = v
	savePending(d)
	return v
}

func (d *Dispatch) scope() string {
	if d.Epic != "" {
		return d.Epic
	}
	return d.ID
}

func loadPending() *Dispatch {
	s, ok := readFile(statePath("pending.json"))
	if !ok {
		return nil
	}
	var d Dispatch
	if json.Unmarshal([]byte(s), &d) != nil || d.Phase == "" {
		return nil
	}
	return &d
}

func savePending(d *Dispatch) {
	b, _ := json.MarshalIndent(d, "", "  ")
	writeState("pending.json", string(b)+"\n")
}

func clearPending() { os.Remove(statePath("pending.json")) }

func currentHarness() string {
	s, _ := readFile(statePath("harness"))
	h := strings.TrimSpace(s)
	for _, x := range harnesses {
		if h == x {
			return h
		}
	}
	fail("no-harness: run preflight --harness claude|codex first")
	return ""
}

// ---------------------------------------------------------------- models

// Models come only from the user (an AGENTS.md section or a saved answer);
// nothing inherits the session model. Optional rows fall back to a base row.
var (
	roles         = []string{"plan", "do", "check", "act"}
	optionalRoles = [][2]string{{"do_L", "do"}, {"check_L", "check"}, {"epic_check", "check"}, {"epic_act", "act"}, {"merge", "check"}}
	claudeModels  = set("opus", "sonnet", "haiku", "fable") // what the Agent tool's model accepts
	// what the Agent tool's effort and Codex's reasoning_effort accept
	effortValues = map[string][]string{
		"claude": {"low", "medium", "high", "xhigh", "max"},
		"codex":  {"minimal", "low", "medium", "high", "xhigh", "max"},
	}
	sectionRE  = regexp.MustCompile(`(?i)^#{1,6}\s*PDCA models:\s*(\w+)\s*$`)
	rowRE      = regexp.MustCompile(`^\s*[-*]\s*(\w+)\s*[:=]\s*(\S+)\s+(\S+)\s*$`)
	reviewRE   = regexp.MustCompile(`(?i)^\s*[-*]\s*review\s*[:=]\s*(\S+)\s*$`)
	parallelRE = regexp.MustCompile(`(?i)^\s*[-*]\s*parallel\s*[:=]\s*(\S+)\s*$`)
)

type ModelSpec struct{ Model, Effort string }

type Models map[string]ModelSpec

type rows struct {
	order    []string
	m        map[string]ModelSpec
	review   string // "- review: epic|task"; empty means epic
	parallel string // "- parallel: yes|no"; empty means no
}

// Mode is how an epic runs: its review mode, and whether independent tasks
// run at once (the planner decides how many by how it splits the work).
type Mode struct {
	Review   string
	Parallel bool
}

func (m Mode) String() string {
	s := "review=" + m.Review
	if m.Parallel {
		s += " parallel=yes"
	}
	return s
}

func (r *rows) put(role string, s ModelSpec) {
	if _, ok := r.m[role]; !ok {
		r.order = append(r.order, role)
	}
	r.m[role] = s
}

// sectionRows returns the rows of the '## PDCA models: <harness>' section,
// or nil when the text has no such section.
func sectionRows(text, harness string) *rows {
	var r *rows
	inside := false
	for _, line := range strings.Split(text, "\n") {
		if m := sectionRE.FindStringSubmatch(line); m != nil {
			inside = strings.EqualFold(m[1], harness)
			if inside && r == nil {
				r = &rows{m: map[string]ModelSpec{}}
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			inside = false
		} else if inside {
			if m := reviewRE.FindStringSubmatch(line); m != nil {
				r.review = strings.ToLower(m[1])
			} else if m := parallelRE.FindStringSubmatch(line); m != nil {
				r.parallel = strings.ToLower(m[1])
			} else if m := rowRE.FindStringSubmatch(line); m != nil {
				r.put(m[1], ModelSpec{m[2], m[3]})
			}
		}
	}
	return r
}

func allRoleNames() []string {
	names := append([]string{}, roles...)
	for _, o := range optionalRoles {
		names = append(names, o[0])
	}
	return names
}

func validateModels(r *rows, harness, src string) Models {
	var problems []string
	for _, role := range roles {
		if _, ok := r.m[role]; !ok {
			problems = append(problems, role+" missing")
		}
	}
	known := set(allRoleNames()...)
	models := []string{}
	for m := range claudeModels {
		models = append(models, m)
	}
	sort.Strings(models)
	for _, role := range r.order {
		s := r.m[role]
		if !known[role] {
			problems = append(problems, fmt.Sprintf("unknown row %q (rows: %s)", role, strings.Join(allRoleNames(), ", ")))
		}
		if harness == "claude" && !claudeModels[s.Model] {
			problems = append(problems, fmt.Sprintf("%s: model %q (the Agent tool takes %s)", role, s.Model, strings.Join(models, ", ")))
		}
		if efforts := effortValues[harness]; !slices.Contains(efforts, s.Effort) {
			problems = append(problems, fmt.Sprintf("%s: effort %q (%s takes %s)", role, s.Effort, harness, strings.Join(efforts, ", ")))
		}
	}
	if r.review != "" && r.review != "epic" && r.review != "task" {
		problems = append(problems, fmt.Sprintf("review %q (epic or task)", r.review))
	}
	if r.parallel != "" && r.parallel != "yes" && r.parallel != "no" {
		problems = append(problems, fmt.Sprintf("parallel %q (yes or no; the planner decides how many tasks run at once)", r.parallel))
	}
	if len(problems) > 0 {
		fail("models %s: %s", src, strings.Join(problems, "; "))
	}
	full := Models{}
	for role, s := range r.m {
		full[role] = s
	}
	for _, o := range optionalRoles {
		if _, ok := full[o[0]]; !ok {
			full[o[0]] = r.m[o[1]]
		}
	}
	return full
}

func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func claudeHome() string {
	if h := os.Getenv("CLAUDE_CONFIG_DIR"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// agentsMdFiles: AGENTS.md from the repo root up to /, then ~/.codex/AGENTS.md.
func agentsMdFiles() []string {
	d := repoRoot()
	if d == "" {
		d, _ = os.Getwd()
	}
	var files []string
	for {
		files = append(files, filepath.Join(d, "AGENTS.md"))
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return append(files, filepath.Join(codexHome(), "AGENTS.md"))
}

func modelsSectionFile(harness string) (string, *rows) {
	for _, p := range agentsMdFiles() {
		if text, ok := readFile(p); ok {
			if r := sectionRows(text, harness); r != nil {
				return p, r
			}
		}
	}
	return "", nil
}

func loadModels(harness string) (string, Models) {
	src, m, _ := loadConfig(harness)
	return src, m
}

// loadConfig returns the models and the mode: review epic unless the section
// says "- review: task", one Do at a time unless it says "- parallel: yes".
func loadConfig(harness string) (string, Models, Mode) {
	mode := func(r *rows) Mode {
		m := Mode{Review: r.review, Parallel: r.parallel == "yes"}
		if m.Review == "" {
			m.Review = "epic"
		}
		return m
	}
	if p, r := modelsSectionFile(harness); p != "" {
		return p, validateModels(r, harness, p), mode(r)
	}
	saved := statePath("models-" + harness)
	if text, ok := readFile(saved); ok {
		r := sectionRows("# PDCA models: "+harness+"\n"+text, harness)
		return saved, validateModels(r, harness, saved), mode(r)
	}
	fail("no-models: no '## PDCA models: %s' section in AGENTS.md and no saved answer; "+
		"ask the user for the model and effort of plan, do, check and act, then run: "+
		"%s models --harness %s --set \"plan=<model>/<effort> do=... check=... act=...\"", harness, scriptPath(), harness)
	return "", nil, Mode{}
}

func modelsLine(m Models, mode Mode) string {
	var parts []string
	for _, role := range allRoleNames() {
		if role != "merge" || mode.Parallel {
			parts = append(parts, fmt.Sprintf("%s=%s/%s", role, m[role].Model, m[role].Effort))
		}
	}
	return "MODELS " + strings.Join(parts, " ") + " " + mode.String()
}

// ---------------------------------------------------------------- agents

// Phase agents: the phase file is the agent's whole instruction set, so a
// dispatch prompt carries only IDs. Claude Code tools are kept to what the
// phase needs, because every tool schema is re-read on every request.
// the codebase-memory code graph, for the phases that explore code
const graphTools = ", mcp__codebase-memory-mcp__search_graph, mcp__codebase-memory-mcp__trace_path, mcp__codebase-memory-mcp__get_code_snippet"

var agentSpecs = []struct{ phase, file, tools string }{
	{"plan", "plan.md", "Bash, Read, Grep, Glob" + graphTools},
	{"do", "do.md", "Bash, Read, Edit, Write, Grep, Glob"},
	{"check", "check.md", "Bash, Read, Grep, Glob" + graphTools},
	{"act", "act.md", "Bash, Read, Grep, Glob"},
	{"merge", "merge.md", "Bash, Read, Edit, Write, Grep, Glob"}, // only after a parallel batch
}

func agentBody(file string) string {
	text, ok := readFile(filepath.Join(skillDir(), "phases", file))
	if !ok {
		fail("missing phase file phases/%s", file)
	}
	text = strings.ReplaceAll(text, "<SKILL_DIR>", skillDir())
	return fmt.Sprintf("`pdca` below means `%s`.\n\n%s", scriptPath(), text)
}

func agentDescription(phase string) string {
	return fmt.Sprintf("PDCA %s phase, dispatched by the pdca skill.", phase)
}

// agentFile returns the path and content of a phase agent for a harness.
func agentFile(harness, phase, file, tools, dir string) (string, string) {
	desc := agentDescription(phase)
	body := agentBody(file)
	if harness == "claude" {
		return filepath.Join(dir, "pdca-"+phase+".md"),
			fmt.Sprintf("---\nname: pdca-%s\ndescription: %s\ntools: %s\n---\n\n%s", phase, desc, tools, body)
	}
	if strings.Contains(body, "'''") {
		fail("phases/%s contains ''' and can't be a TOML literal string", file)
	}
	return filepath.Join(dir, "pdca-"+phase+".toml"),
		fmt.Sprintf("name = \"pdca-%s\"\ndescription = \"%s\"\ndeveloper_instructions = '''\n%s'''\n", phase, desc, body)
}

// agentDir is where the harness reads the phase agents from. Claude Code:
// the project's .claude/agents, so projects that don't use pdca never see
// them. Codex reads only ~/.codex/agents (codex-cli 0.166 ignores a project's
// .codex/agents, trusted or not).
func agentDir(harness string) string {
	if harness == "codex" {
		return filepath.Join(codexHome(), "agents")
	}
	root := repoRoot()
	if root == "" {
		fail("not inside a git repository")
	}
	return filepath.Join(root, ".claude", "agents")
}

// sessionAgents is the phase agents as the JSON that `claude --agents`
// takes: agents for one session, written nowhere.
func sessionAgents() string {
	m := map[string]any{}
	for _, a := range agentSpecs {
		m["pdca-"+a.phase] = map[string]any{"description": agentDescription(a.phase), "prompt": agentBody(a.file),
			"tools": strings.Split(a.tools, ", ")}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func agentsProblem(harness string) string {
	if harness == "claude" && os.Getenv("PDCA_AGENTS") == "session" {
		return "" // pdca once started this session with claude --agents
	}
	d := agentDir(harness)
	var stale []string
	found := false
	for _, a := range agentSpecs {
		p, text := agentFile(harness, a.phase, a.file, a.tools, d)
		have, ok := readFile(p)
		found = found || ok
		if !ok || have != text {
			stale = append(stale, "pdca-"+a.phase)
		}
	}
	fix := scriptPath() + " install"
	if harness == "claude" {
		fix += " --project"
	}
	switch {
	case len(stale) == 0:
		return ""
	case found:
		return fmt.Sprintf("agents-stale: %s in %s differ from the phase files; run %s, then start a new session",
			strings.Join(stale, ", "), d, fix)
	case harness == "claude":
		return fmt.Sprintf("no-agents: this project isn't set up for pdca. To keep using pdca here: %s init (or /pdca init), "+
			"then a new session. For a single run: leave this session and start `pdca once \"<task>\"` in a terminal in the repo", scriptPath())
	}
	return fmt.Sprintf("no-agents: run %s, then start a new session so Codex loads the pdca-* agents", fix)
}
