package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Dep is a dependency as `bd show` (an issue plus dependency_type) or
// `bd list` (an edge: issue_id, depends_on_id, type) reports it.
type Dep struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	DependencyType string `json:"dependency_type"`
	DependsOnID    string `json:"depends_on_id"`
	Type           string `json:"type"`
}

type Issue struct {
	ID                 string          `json:"id"`
	Title              string          `json:"title"`
	Description        string          `json:"description"`
	Design             string          `json:"design"`
	AcceptanceCriteria string          `json:"acceptance_criteria"`
	Notes              string          `json:"notes"`
	Status             string          `json:"status"`
	IssueType          string          `json:"issue_type"`
	Priority           int             `json:"priority"`
	Parent             string          `json:"parent"`
	RawMeta            json.RawMessage `json:"metadata"`
	Dependencies       []Dep           `json:"dependencies"`
	meta               map[string]string
}

func (i *Issue) Meta(key string) string {
	if i.meta == nil {
		i.meta = parseMeta(i.RawMeta)
	}
	return i.meta[key]
}

// parseMeta reads bead metadata, which bd returns as an object or as a JSON
// string; every value becomes a string.
func parseMeta(raw json.RawMessage) map[string]string {
	m := map[string]string{}
	decode := func(b []byte) map[string]any {
		var obj map[string]any
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if dec.Decode(&obj) != nil {
			return nil
		}
		return obj
	}
	obj := decode(raw)
	if obj == nil {
		var s string
		if json.Unmarshal(raw, &s) != nil || s == "" {
			return m
		}
		if obj = decode([]byte(s)); obj == nil {
			return m
		}
	}
	for k, v := range obj {
		switch x := v.(type) {
		case nil:
		case string:
			m[k] = x
		default:
			m[k] = fmt.Sprint(x)
		}
	}
	return m
}

type Comment struct {
	Text string `json:"text"`
}

// bdLabel names a bd call in errors: the subcommand and its first positional.
func bdLabel(args []string) string {
	label := args[0]
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		label += " " + args[1]
	}
	return label
}

func bdRaw(args ...string) string {
	rc, o, e := run("bd", args...)
	if rc != 0 {
		fail("bd %s: %s", bdLabel(args), firstLine(e, o))
	}
	return o
}

// decodeJSON decodes the first JSON value of bd's output into v, skipping the
// warning lines bd may print before it, even ones that start with a bracket.
func decodeJSON(o string, v any) bool {
	for rest := o; ; {
		if t := strings.TrimLeft(rest, " \t\r"); strings.HasPrefix(t, "[") || strings.HasPrefix(t, "{") {
			var raw json.RawMessage
			if json.NewDecoder(strings.NewReader(t)).Decode(&raw) == nil {
				return json.Unmarshal(raw, v) == nil
			}
		}
		_, after, ok := strings.Cut(rest, "\n")
		if !ok {
			return false
		}
		rest = after
	}
}

func bdJSON(v any, args ...string) {
	o := bdRaw(append(args, "--json")...)
	if strings.TrimSpace(o) == "" {
		return
	}
	if !decodeJSON(o, v) {
		fail("bd %s: non-JSON output", bdLabel(args))
	}
}

// tryShow returns the bead, or nil when bd can't show it.
func tryShow(id string) *Issue {
	rc, o, _ := run("bd", "show", id, "--json")
	var raw json.RawMessage
	if rc != 0 || !decodeJSON(o, &raw) {
		return nil
	}
	var list []Issue
	if json.Unmarshal(raw, &list) == nil {
		if len(list) == 0 {
			return nil
		}
		return &list[0]
	}
	var one Issue
	if json.Unmarshal(raw, &one) != nil || one.ID == "" {
		return nil
	}
	return &one
}

func show(id string) *Issue {
	iss := tryShow(id)
	if iss == nil {
		fail("bead %s not found", id)
	}
	return iss
}

func exists(id string) bool {
	rc, _, _ := run("bd", "show", id, "--json")
	return rc == 0
}

func comments(id string) []Comment {
	var cs []Comment
	bdJSON(&cs, "comments", id)
	return cs
}

func children(epic string) []*Issue {
	var list []Issue
	bdJSON(&list, "children", epic)
	var kids []*Issue
	for i := range list {
		if list[i].IssueType != "epic" {
			kids = append(kids, &list[i])
		}
	}
	sortIssues(kids)
	return kids
}

func readyIn(epic string) []*Issue {
	var list []Issue
	bdJSON(&list, "ready", "--parent", epic, "--exclude-type", "epic")
	var r []*Issue
	for i := range list {
		r = append(r, &list[i])
	}
	return r
}

// dependents lists open tasks that are blocked by id.
func dependents(id string) []Dep {
	var list []Dep
	bdJSON(&list, "dep", "list", id, "--direction=up", "--type", "blocks")
	var open []Dep
	for _, d := range list {
		if d.Status != "closed" {
			open = append(open, d)
		}
	}
	return open
}

type newIssue struct {
	title, typ, parent, description, design, acceptance, notes string
	deps, labels                                               []string
	meta                                                       map[string]string
}

func createIssue(n newIssue) string {
	args := []string{"create", "--title", n.title, "-t", n.typ, "--silent"}
	var temps []string
	defer func() {
		for _, t := range temps {
			os.Remove(t)
		}
	}()
	addFile := func(flag, text string) {
		if strings.TrimSpace(text) == "" {
			return
		}
		p := tempFile(text)
		temps = append(temps, p)
		args = append(args, flag, p)
	}
	addFile("--body-file", n.description)
	addFile("--design-file", n.design)
	if n.parent != "" {
		args = append(args, "--parent", n.parent)
	}
	if n.acceptance != "" {
		args = append(args, "--acceptance", n.acceptance)
	}
	if n.notes != "" {
		args = append(args, "--notes", n.notes)
	}
	if len(n.deps) > 0 {
		args = append(args, "--deps", strings.Join(n.deps, ","))
	}
	if len(n.labels) > 0 {
		args = append(args, "-l", strings.Join(n.labels, ","))
	}
	if len(n.meta) > 0 {
		b, _ := json.Marshal(n.meta)
		args = append(args, "--metadata", string(b))
	}
	o := bdRaw(args...)
	lines := strings.Fields(o)
	if len(lines) == 0 {
		fail("bd create %q printed no id", n.title)
	}
	id := lines[len(lines)-1]
	created = append(created, id)
	return id
}

// created lists the beads made inside atomically, for its rollback.
var created []string

// atomically runs f, which creates beads; if f fails midway, the beads it
// created are deleted again, newest first, so a rerun doesn't find half a
// plan.
func atomically(f func()) {
	created = nil
	defer func() {
		r := recover()
		if r == nil {
			created = nil
			return
		}
		ids := created
		created = nil
		for i := len(ids) - 1; i >= 0; i-- {
			run("bd", "delete", ids[i], "--force")
		}
		if fl, ok := r.(failure); ok && len(ids) > 0 {
			fl.msg += "; deleted the beads this call had created (" + strings.Join(ids, ", ") + "), so it can be rerun"
			panic(fl)
		}
		panic(r)
	}()
	f()
}

// bdUpdate sets and unsets metadata and appends a note in one bd call (each
// call costs 250-850 ms).
func bdUpdate(id string, setKV, unset []string, note string) {
	args := []string{"update", id}
	for _, p := range setKV {
		args = append(args, "--set-metadata", p)
	}
	for _, k := range unset {
		args = append(args, "--unset-metadata", k)
	}
	if note != "" {
		args = append(args, "--append-notes", note)
	}
	bdRaw(args...)
}

func setMeta(id string, kv ...string) { bdUpdate(id, kv, nil, "") }

func withTemp(text string, f func(path string)) {
	p := tempFile(text)
	defer os.Remove(p)
	f(p)
}

func bdNote(id, text string) {
	withTemp(text, func(p string) { bdRaw("note", id, "--file", p) })
}

func bdComment(id, text string) {
	withTemp(text, func(p string) { bdRaw("comment", id, "--file", p) })
}

func bdClose(id, reason string) { bdRaw("close", id, "--reason", reason) }

func bdClaim(id string) { bdRaw("update", id, "--claim") }

func bdRemember(text string) { bdRaw("remember", text) }

// newestComment returns the newest comment starting with prefix.
func newestComment(cs []Comment, prefix string) string {
	for i := len(cs) - 1; i >= 0; i-- {
		t := strings.TrimSpace(cs[i].Text)
		if strings.HasPrefix(t, prefix) {
			return t
		}
	}
	return ""
}
