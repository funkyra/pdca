package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
)

var protectedBranches = set("main", "master", "trunk", "develop", "dev")

// invocation is how the user starts the skill in a harness.
func invocation(harness string) string {
	if harness == "codex" {
		return "$pdca"
	}
	return "/pdca"
}

func cmdPreflight(args []string) int {
	flags, pos := parseArgs("preflight", args, map[string]bool{"--harness": true, "--branch": true})
	harness := flags["--harness"]
	if len(pos) > 0 || (harness != "claude" && harness != "codex") {
		usageFail("preflight")
	}
	root := repoRoot()
	if root == "" {
		fail("no-git: not inside a git repository")
	}
	base := headSHA()
	if base == "" {
		fail("no-commits: repository has no commits yet; make an initial commit")
	}
	if rc, _, _ := run("bd", "where"); rc != 0 {
		fail("no-beads: this project has no beads workspace; the user sets the project up with %s init (it runs bd init) - stop and tell the user", invocation(harness))
	}
	if dirty := dirtyPaths(); len(dirty) > 0 {
		fail("dirty-tree: %d uncommitted path(s): %s", len(dirty), listShort(dirty, 10))
	}
	if p := agentsProblem(harness); p != "" {
		fail("%s", p)
	}
	_, models, mode := loadConfig(harness)
	writeState("harness", harness+"\n")
	branch := currentBranch()
	protected := map[string]bool{}
	for k := range protectedBranches {
		protected[k] = true
	}
	if rc, o, _ := git("symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); rc == 0 {
		if _, b, ok := strings.Cut(strings.TrimSpace(o), "/"); ok {
			protected[b] = true
		}
	}
	onProtected := branch == "" || protected[branch]
	want := flags["--branch"]
	switch {
	case want != "" && onProtected:
		if rc, _, _ := git("show-ref", "--verify", "--quiet", "refs/heads/"+want); rc == 0 {
			gitOK("switch", want)
		} else {
			gitOK("switch", "-c", want)
		}
		branch, base = want, headSHA()
	case onProtected:
		name := branch
		if name == "" {
			name = "detached HEAD"
		}
		say(fmt.Sprintf("WARN on-protected-branch: '%s'; rerun with --branch pdca/<slug> before planning", name))
	}
	say(fmt.Sprintf("OK root=%s branch=%s base=%s", root, branch, base), modelsLine(models, mode))
	var epics []Issue
	bdJSON(&epics, "list", "-t", "epic", "--status", "open")
	for i := range epics {
		if e := &epics[i]; e.Meta("pdca_plan") == "approved" {
			say(fmt.Sprintf("OPEN %s branch=%s %s", e.ID, orDash(e.Meta("pdca_branch")), e.Title))
		}
	}
	return 0
}

var plainArgRE = regexp.MustCompile(`^[A-Za-z0-9_./@%+=:,-]+$`)

// shellQuote quotes a path for a command the user copies.
func shellQuote(s string) string {
	if plainArgRE.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// pdcaHome is the home directory for the user-level links (PDCA_HOME
// overrides it, for tests).
func pdcaHome() string {
	if h := os.Getenv("PDCA_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

// writeIfChanged writes text to path unless it already holds it.
func writeIfChanged(path, text string) bool {
	if have, ok := readFile(path); ok && have == text {
		return false
	}
	writeFile(path, text)
	return true
}

// link makes path a symlink to target. Something else already at path is
// left alone and reported.
func link(path, target string) (made bool, warn string) {
	if _, err := os.Lstat(path); err == nil {
		have, err1 := filepath.EvalSymlinks(path)
		want, err2 := filepath.EvalSymlinks(target)
		if err1 == nil && err2 == nil && have == want {
			return false, ""
		}
		return false, fmt.Sprintf("WARN left %s alone: it exists and doesn't lead to %s", path, target)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fail("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.Symlink(target, path); err != nil {
		fail("link %s: %v", path, err)
	}
	return true, ""
}

// writeAgents writes the phase agents of a harness into dir and reports how
// many files changed.
func writeAgents(harness, dir string) []string {
	n := 0
	for _, a := range agentSpecs {
		if writeIfChanged(agentFile(harness, a.phase, a.file, a.tools, dir)) {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return []string{fmt.Sprintf("OK installed %d pdca-* agent file(s) in %s", n, dir)}
}

// installUser links the skill and command. Phase agents are installed per project.
func installUser() (changed, warns []string) {
	for _, a := range agentSpecs {
		p := filepath.Join(claudeHome(), "agents", "pdca-"+a.phase+".md")
		if text, ok := readFile(p); ok && strings.HasPrefix(text, "---\nname: pdca-"+a.phase+"\n") && os.Remove(p) == nil {
			changed = append(changed, "OK removed "+p+" (Claude Code agents now live in the projects)")
		}
	}
	for _, l := range [][2]string{
		{filepath.Join(claudeHome(), "skills", "pdca"), skillDir()},
		{filepath.Join(pdcaHome(), ".agents", "skills", "pdca"), skillDir()},
		{filepath.Join(pdcaHome(), ".local", "bin", "pdca"), scriptPath()},
	} {
		made, warn := link(l[0], l[1])
		if warn != "" {
			warns = append(warns, warn)
		}
		if made {
			changed = append(changed, "OK installed "+l[0]+" -> "+l[1])
		}
	}
	return changed, warns
}

// installProject generates phase agents with machine-specific paths and
// excludes the generated files from git status.
func installProject(harness string) (changed, warns []string) {
	changed = writeAgents(harness, agentDir(harness))
	ext := ".md"
	if harness == "codex" {
		ext = ".toml"
	}
	pattern := "/." + harness + "/agents/pdca-*" + ext
	rc, p, e := git("rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	text, _ := readFile(p)
	if rc == 0 && slices.Contains(strings.Split(text, "\n"), pattern) {
		return changed, nil
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	var err error
	if rc == 0 {
		err = os.WriteFile(p, []byte(text+pattern+"\n"), 0o644)
	}
	if rc != 0 || err != nil {
		warns = append(warns, fmt.Sprintf("WARN couldn't add %s to .git/info/exclude (%s); add it to .gitignore", pattern, firstLine(e, fmt.Sprint(err))))
	}
	return changed, warns
}

func cmdInstall(args []string) int {
	flags, pos := parseArgs("install", args, map[string]bool{"--project": false, "--harness": true})
	if len(pos) > 0 {
		usageFail("install")
	}
	var changed, warns []string
	if _, project := flags["--project"]; project {
		harness := flags["--harness"]
		if harness == "" {
			harness = "claude"
		}
		if harness != "claude" && harness != "codex" {
			usageFail("install")
		}
		changed, warns = installProject(harness)
	} else {
		if flags["--harness"] != "" {
			usageFail("install")
		}
		changed, warns = installUser()
	}
	say(warns...)
	if len(changed) == 0 {
		say("OK already installed and current")
		return 0
	}
	say(changed...)
	say("start a new session so the harness loads the skill and the agents")
	return 0
}

// cmdOnce starts a new session with Claude Code session agents or
// project-local Codex agents. Beads stores the plan.
func cmdOnce(args []string) int {
	var extra []string
	for i, a := range args {
		if a == "--" {
			args, extra = args[:i], args[i+1:]
			break
		}
	}
	flags, pos := parseArgs("once", args, map[string]bool{"--harness": true})
	harness := flags["--harness"]
	if harness == "" {
		harness = "claude"
	}
	if len(pos) == 0 || (harness != "claude" && harness != "codex") {
		usageFail("once")
	}
	if repoRoot() == "" {
		fail("no-git: not inside a git repository")
	}
	if headSHA() == "" {
		fail("no-commits: make a first commit, then rerun")
	}
	if rc, _, _ := run("bd", "where"); rc != 0 {
		bdInit(repoRoot(), false)
		say("OK beads initialized (bd init): the plan and the state of the run live there")
	}
	name, argv, env := onceCommand(harness, strings.Join(pos, " "), extra)
	path, err := exec.LookPath(name)
	if err != nil {
		fail("%s is not on PATH", name)
	}
	out.Flush()
	if err := syscall.Exec(path, argv, env); err != nil {
		fail("exec %s: %v", name, err)
	}
	return 0
}

// onceCommand is the harness session pdca once starts: the program, its
// argv (extra are the user's own flags, such as --model) and environment.
func onceCommand(harness, task string, extra []string) (string, []string, []string) {
	if harness == "codex" {
		changed, warns := installProject("codex")
		say(warns...)
		say(changed...)
		return "codex", append(append([]string{"codex"}, extra...), "$pdca "+task), os.Environ()
	}
	argv := append(append([]string{"claude", "--agents", sessionAgents()}, extra...), "/pdca "+task)
	return "claude", argv, append(os.Environ(), "PDCA_AGENTS=session")
}

func parseModelSpec(spec string) *rows {
	r := &rows{m: map[string]ModelSpec{}}
	for _, item := range strings.Fields(spec) {
		role, val, _ := strings.Cut(item, "=")
		switch role {
		case "review":
			r.review = strings.ToLower(val)
			continue
		case "parallel":
			r.parallel = strings.ToLower(val)
			continue
		}
		i := strings.LastIndex(val, "/")
		if role == "" || i <= 0 || i == len(val)-1 {
			fail("bad item %q; expected role=model/effort", item)
		}
		r.put(role, ModelSpec{val[:i], val[i+1:]})
	}
	return r
}

// writeModeRows writes the review and parallel rows the user gave.
func writeModeRows(b *strings.Builder, r *rows) {
	if r.review != "" {
		fmt.Fprintf(b, "- review: %s\n", r.review)
	}
	if r.parallel != "" {
		fmt.Fprintf(b, "- parallel: %s\n", r.parallel)
	}
}

// writeModelsSection puts the '## PDCA models: <harness>' section into an
// AGENTS.md file, replacing the old one in place or appending it.
func writeModelsSection(path, harness string, r *rows) {
	var b strings.Builder
	fmt.Fprintf(&b, "## PDCA models: %s\n", harness)
	for _, role := range r.order {
		fmt.Fprintf(&b, "- %s: %s %s\n", role, r.m[role].Model, r.m[role].Effort)
	}
	writeModeRows(&b, r)
	block := strings.TrimRight(b.String(), "\n")
	text, _ := readFile(path)
	var lines, kept []string
	inside, replaced := false, false
	// only the old rows go; anything else the user wrote in the section stays
	flush := func() {
		for len(kept) > 0 && strings.TrimSpace(kept[0]) == "" {
			kept = kept[1:]
		}
		for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
			kept = kept[:len(kept)-1]
		}
		if len(kept) > 0 {
			lines = append(append(lines, ""), kept...)
		}
		kept = nil
	}
	for _, line := range strings.Split(text, "\n") {
		if m := sectionRE.FindStringSubmatch(line); m != nil && strings.EqualFold(m[1], harness) {
			if !replaced {
				lines = append(lines, block)
				replaced = true
			}
			inside = true
			continue
		}
		if inside {
			if !strings.HasPrefix(strings.TrimSpace(line), "#") {
				if !rowRE.MatchString(line) && !reviewRE.MatchString(line) && !parallelRE.MatchString(line) {
					kept = append(kept, line)
				}
				continue
			}
			inside = false
			flush()
			lines = append(lines, "")
		}
		lines = append(lines, line)
	}
	flush()
	res := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if !replaced {
		if res != "" {
			res += "\n\n"
		}
		res += block
	}
	writeFile(path, res+"\n")
}

// bdInit makes the beads workspace in root. bd init's own agent setup also
// writes Cursor rules and CLAUDE.md, so it is skipped. With agents, only
// the Codex recipe (AGENTS.md, .agents/skills/beads, .codex hooks; agy reads
// the first two too) and the Claude Code SessionStart hook are added. The
// claude recipe also writes CLAUDE.md, which is put back as it was: Claude
// Code reads AGENTS.md.
func bdInit(root string, agents bool) {
	if rc, o, e := runIn(root, "bd", "init", "--non-interactive", "--skip-agents"); rc != 0 {
		fail("bd init: %s", firstLine(e, o))
	}
	if agents {
		bdSetup(root)
	}
}

// bdSetup adds bd's Codex and Claude Code setup and keeps CLAUDE.md as it was.
func bdSetup(root string) {
	claudeMd := filepath.Join(root, "CLAUDE.md")
	before, had := readFile(claudeMd)
	for _, recipe := range []string{"codex", "claude"} {
		if rc, o, e := runIn(root, "bd", "setup", recipe); rc != 0 {
			fail("bd setup %s: %s", recipe, firstLine(e, o))
		}
	}
	if had {
		writeIfChanged(claudeMd, before)
	} else {
		os.Remove(claudeMd)
	}
}

// cmdInit sets up project agents, Beads, and the models section of the
// project's AGENTS.md. Models come only
// from the user: without --models and without a section, it says what to ask.
func cmdInit(args []string) int {
	flags, pos := parseArgs("init", args, map[string]bool{"--harness": true, "--models": true, "--review": true})
	harness := flags["--harness"]
	if harness == "" {
		harness = "claude"
	}
	if len(pos) > 0 || (harness != "claude" && harness != "codex") {
		usageFail("init")
	}
	if flags["--review"] != "" && flags["--models"] == "" {
		fail("--review goes with --models (it is a row of the same AGENTS.md section)")
	}
	root := repoRoot()
	fresh := root == "" || headSHA() == ""
	if fresh {
		dir := root
		if dir == "" {
			dir, _ = os.Getwd()
			if dir == pdcaHome() || dir == filepath.Dir(dir) {
				fail("no-git: won't create a git repository in %s; run init in the project directory", dir)
			}
		}
		bdInit(dir, false) // bd init also runs git init and makes the first commit, of its own files
		if root = repoRoot(); root == "" || headSHA() == "" {
			fail("no-commits: bd init made no commit; make a first commit, then rerun init")
		}
		say("OK git repository set up by bd init")
	}
	dirtyBefore := set(statusPaths()...)
	if harness == "claude" {
		func() {
			defer func() {
				if r := recover(); r != nil {
					f, ok := r.(failure)
					if !ok {
						panic(r)
					}
					say("WARN couldn't update the user-level install (" + f.msg + "); run " + scriptPath() + " install outside the sandbox")
				}
			}()
			changed, warns := installUser()
			say(warns...)
			say(changed...)
		}()
	}
	changed, warns := installProject(harness)
	say(warns...)
	say(changed...)
	newAgents := len(changed) > 0
	if rc, _, _ := run("bd", "where"); rc == 0 && !fresh {
		say("OK beads already set up")
	} else {
		if fresh {
			bdSetup(root)
		} else {
			bdInit(root, true)
		}
		say("OK beads initialized (bd init) with its Codex and Claude Code setup")
	}
	missing, wroteAgentsMd := false, false
	if spec := flags["--models"]; spec != "" {
		r := parseModelSpec(spec)
		if v := flags["--review"]; v != "" {
			r.review = strings.ToLower(v)
		}
		validateModels(r, harness, "--models")
		p := filepath.Join(root, "AGENTS.md")
		writeModelsSection(p, harness, r)
		wroteAgentsMd = true
		_, m, mode := loadConfig(harness)
		say("OK models written to "+p, modelsLine(m, mode))
	} else if p, _ := modelsSectionFile(harness); p != "" {
		_, m, mode := loadConfig(harness)
		say("OK models from "+p, modelsLine(m, mode))
	} else {
		missing = true
	}
	// only what init itself changed; the user's own edits are not ours to commit
	var paths []string
	for _, p := range statusPaths() {
		if !dirtyBefore[p] || (wroteAgentsMd && p == "AGENTS.md") {
			paths = append(paths, shellQuote(p))
		}
	}
	if len(paths) > 0 {
		say("COMMIT before " + invocation(harness) + " <task> (it needs a clean tree): git add " + strings.Join(paths, " ") + " && git commit -m 'chore: set up pdca'")
	}
	if missing {
		say(fmt.Sprintf("FAIL no-models: ask the user for the model and effort of plan, do, check and act, and the review mode "+
			"(epic: one review of the whole epic, the default; task: one per commit), then run: "+
			"%s init --harness %s --models \"plan=<model>/<effort> do=... check=... act=...\" [--review epic|task]", scriptPath(), harness))
		return 1
	}
	if newAgents {
		say("OK project ready; start a new session (it loads the pdca agents), then run " + invocation(harness) + " <task>")
		return 0
	}
	say("OK project ready; run " + invocation(harness) + " <task>")
	return 0
}

func cmdModels(args []string) int {
	flags, pos := parseArgs("models", args, map[string]bool{"--harness": true, "--set": true})
	if len(pos) > 0 {
		usageFail("models")
	}
	harness := flags["--harness"]
	if harness == "" {
		harness = currentHarness()
	}
	if harness != "claude" && harness != "codex" {
		usageFail("models")
	}
	if spec, ok := flags["--set"]; ok {
		if p, _ := modelsSectionFile(harness); p != "" {
			fail("models come from %s; the user edits that section", p)
		}
		r := parseModelSpec(spec)
		validateModels(r, harness, "--set")
		var b strings.Builder
		for _, role := range r.order {
			fmt.Fprintf(&b, "- %s: %s %s\n", role, r.m[role].Model, r.m[role].Effort)
		}
		writeModeRows(&b, r)
		writeState("models-"+harness, b.String())
	}
	src, models, mode := loadConfig(harness)
	say(fmt.Sprintf("OK harness=%s source=%s", harness, src), modelsLine(models, mode))
	return 0
}

func currentEpic() string {
	branch := currentBranch()
	var epics []Issue
	bdJSON(&epics, "list", "-t", "epic", "--status", "open")
	for i := range epics {
		if epics[i].Meta("pdca_branch") == branch {
			return epics[i].ID
		}
	}
	return ""
}

func cmdCurrent(args []string) int {
	if len(args) > 0 {
		usageFail("current")
	}
	if id := currentEpic(); id != "" {
		say(fmt.Sprintf("EPIC %s %s", id, show(id).Title))
		return 0
	}
	say(fmt.Sprintf("NONE no open PDCA epic for branch '%s'", currentBranch()))
	return 0
}

func cmdStatus(args []string) int {
	_, pos := parseArgs("status", args, map[string]bool{})
	if len(pos) != 1 {
		usageFail("status")
	}
	e := show(pos[0])
	say(fmt.Sprintf("EPIC %s [%s] %s", e.ID, e.Status, e.Title))
	for _, k := range children(e.ID) {
		say(fmt.Sprintf("  %-14s %-11s state=%-8s round=%-2s commit=%-7s verdict=%-4s %s", k.ID, k.Status, taskState(k),
			orDash(k.Meta("pdca_round")), short(k.Meta("pdca_commit")), orDash(k.Meta("pdca_verdict")), k.Title))
	}
	return 0
}

// decisionLines returns the decision and assumption lines of a bead's notes,
// tagged with the task they belong to.
func decisionLines(notes, task string) []string {
	var out []string
	for _, line := range strings.Split(notes, "\n") {
		if decisRE.MatchString(line) {
			if task != "" {
				line = "[" + task + "] " + strings.TrimSpace(line)
			}
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func cmdReport(args []string) int {
	_, pos := parseArgs("report", args, map[string]bool{})
	if len(pos) != 1 {
		usageFail("report")
	}
	root := show(pos[0])
	if root.IssueType != "epic" {
		fail("%s is not an epic", root.ID)
	}
	head := headSHA()
	base := root.Meta("pdca_base")
	ncommits := "?"
	if base != "" {
		if rc, n, _ := git("rev-list", "--count", base+"..HEAD"); rc == 0 {
			ncommits = n
		}
	}
	kids := children(root.ID)
	say(fmt.Sprintf("EPIC %s [%s] %s", root.ID, root.Status, root.Title),
		fmt.Sprintf("branch=%s base=%s head=%s commits=%s", orDash(root.Meta("pdca_branch")), short(base), short(head), ncommits),
		"TASKS:")
	from := map[string]bool{root.ID: true}
	for _, k := range kids {
		from[k.ID] = true
		act := ""
		if a := k.Meta("pdca_act"); a != "" {
			act = " act=" + a
		}
		say(fmt.Sprintf("  %s [%s] fix_rounds=%s verdict=%s%s - %s", k.ID, k.Status, orDash(k.Meta("pdca_round")),
			orDash(k.Meta("pdca_verdict")), act, k.Title))
	}
	var followups []Issue
	var all []Issue
	if rc, o, _ := run("bd", "list", "--status", "open", "-l", "pdca-followup", "--json"); rc == 0 {
		decodeJSON(o, &all)
	}
	for _, f := range all {
		for _, dep := range f.Dependencies {
			if dep.Type == "discovered-from" && from[dep.DependsOnID] {
				followups = append(followups, f)
				break
			}
		}
	}
	say(fmt.Sprintf("FOLLOWUPS (%d):", len(followups)))
	for _, f := range followups {
		say(fmt.Sprintf("  %s - %s", f.ID, f.Title))
	}
	decisions := decisionLines(root.Notes, "")
	for _, k := range kids {
		decisions = append(decisions, decisionLines(k.Notes, k.ID)...)
	}
	say(fmt.Sprintf("AUTONOMOUS DECISIONS (%d):", len(decisions)))
	for _, d := range decisions {
		say("  " + d)
	}
	return 0
}
