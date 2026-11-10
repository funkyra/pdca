package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// run executes a command and returns its exit code, stdout (trailing
// whitespace trimmed, leading kept: porcelain output starts with spaces) and
// stderr.
func run(name string, args ...string) (int, string, string) {
	return runIn("", name, args...)
}

// runIn is run in another directory ("" = the current one).
func runIn(dir, name string, args ...string) (int, string, string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	rc := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			rc = ee.ExitCode()
		} else {
			rc = 127
			se.WriteString(err.Error())
		}
	}
	return rc, strings.TrimRight(so.String(), " \t\r\n"), strings.TrimSpace(se.String())
}

func firstLine(texts ...string) string {
	for _, t := range texts {
		if t = strings.TrimSpace(t); t != "" {
			return strings.SplitN(t, "\n", 2)[0]
		}
	}
	return "failed"
}

func git(args ...string) (int, string, string) { return run("git", args...) }

func gitOK(args ...string) string {
	rc, o, e := git(args...)
	if rc != 0 {
		fail("git %s: %s", strings.Join(args, " "), firstLine(e, o))
	}
	return o
}

func headSHA() string {
	rc, o, _ := git("rev-parse", "--verify", "--quiet", "HEAD")
	if rc != 0 {
		return ""
	}
	return strings.TrimSpace(o)
}

func repoRoot() string {
	rc, o, _ := git("rev-parse", "--show-toplevel")
	if rc != 0 {
		return ""
	}
	return strings.TrimSpace(o)
}

func currentBranch() string {
	_, o, _ := git("branch", "--show-current")
	return strings.TrimSpace(o)
}

func short(sha string) string {
	if sha == "" {
		return "-"
	}
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func sameSHA(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	n := min(len(a), len(b))
	return n >= 7 && a[:n] == b[:n]
}

func commitsSince(base string) int {
	return atoi(gitOK("rev-list", "--count", base+"..HEAD"))
}

// statusPaths lists every path git status reports: root-relative, unquoted,
// the new name of a rename.
func statusPaths() []string {
	var paths []string
	f := strings.Split(gitOK("--no-optional-locks", "status", "--porcelain", "-z"), "\x00")
	for i := 0; i < len(f); i++ {
		if len(f[i]) < 4 {
			continue
		}
		paths = append(paths, f[i][3:])
		if f[i][0] == 'R' || f[i][0] == 'C' {
			i++ // the next field is the old name
		}
	}
	return paths
}

// dirtyPaths lists uncommitted changes, ignoring beads' own files.
func dirtyPaths() []string {
	var paths []string
	for _, p := range statusPaths() {
		if strings.HasPrefix(p, ".beads/") || strings.HasPrefix(p, ".beads.") || strings.Contains(p, ".gate.lock") {
			continue
		}
		paths = append(paths, p)
	}
	return paths
}

func listShort(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + " ..."
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func itoa(n int) string { return strconv.Itoa(n) }

func readFile(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func writeFile(path, text string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fail("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		fail("write %s: %v", path, err)
	}
}

// tempFile writes text to a fresh temp file and returns its path.
func tempFile(text string) string {
	f, err := os.CreateTemp("", "pdca-*.md")
	if err != nil {
		fail("temp file: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		fail("temp file: %v", err)
	}
	return f.Name()
}

var digitsRE = regexp.MustCompile(`\d+|\D+`)

// naturalLess orders ids like inv-1.2 < inv-1.10.
func naturalLess(a, b string) bool {
	pa, pb := digitsRE.FindAllString(a, -1), digitsRE.FindAllString(b, -1)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] == pb[i] {
			continue
		}
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		switch {
		case ea == nil && eb == nil:
			return na < nb
		case ea == nil:
			return true
		case eb == nil:
			return false
		}
		return pa[i] < pb[i]
	}
	return len(pa) < len(pb)
}

func sortIssues(issues []*Issue) {
	sort.SliceStable(issues, func(i, j int) bool { return naturalLess(issues[i].ID, issues[j].ID) })
}

// splitList splits a comma-joined list; "" is no items.
func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func set(items ...string) map[string]bool {
	m := map[string]bool{}
	for _, it := range items {
		m[it] = true
	}
	return m
}
