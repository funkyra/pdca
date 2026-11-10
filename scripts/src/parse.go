package main

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ---------------------------------------------------------------- plan files

// A plan file is markdown with level-1 blocks:
//
//	# Epic: <title>       ## Description / ## Design / ## Acceptance / ## Decisions
//	# Task: <title>       Size: / Deps: / Type: lines, then ## Description / ## Acceptance
//	# Notes               - <task-id>: <what changed>
//
// Text before the first known "##" heading of a block is its description.
// Unknown "##" headings stay as content of the current section, so a design
// can have its own subsections. Headings inside code fences are content.

type PlanTask struct {
	Title, Type, Size, Description, Design, Acceptance, Decisions string
	Deps                                                          []string
}

type PlanEpic struct {
	Title, Description, Design, Acceptance, Decisions string
}

type Plan struct {
	Epic  *PlanEpic
	Tasks []PlanTask
	Notes []string // "- <id>: <text>" items
}

var (
	blockRE  = regexp.MustCompile(`(?i)^#\s+(epic|task|notes)\b\s*:?\s*(.*?)\s*$`)
	subRE    = regexp.MustCompile(`^##\s+(.+?)\s*:?\s*$`)
	metaRE   = regexp.MustCompile(`(?i)^\**(size|deps|depends on|type)\**\s*:\s*(.*?)\s*$`)
	fenceRE  = regexp.MustCompile("^\\s*(```|~~~)")
	decisRE  = regexp.MustCompile(`(?mi)^\s*[-*]\s*\**\s*(assumption|decision)`)
	depTokRE = regexp.MustCompile(`(?i)^#?t?(\d+)$`)
)

func canonicalSection(kind, name string) string {
	n := strings.ToLower(strings.Trim(strings.TrimSpace(name), "*:"))
	switch n {
	case "description":
		return "description"
	case "design":
		return "design"
	case "acceptance", "acceptance criteria":
		return "acceptance"
	case "decisions", "autonomous decisions":
		return "decisions"
	}
	return ""
}

type block struct {
	kind, title string
	meta        map[string]string
	sec         map[string]*strings.Builder
}

func (b *block) add(sec, line string) {
	if b.sec[sec] == nil {
		b.sec[sec] = &strings.Builder{}
	}
	b.sec[sec].WriteString(line + "\n")
}

func (b *block) get(sec string) string {
	if b.sec[sec] == nil {
		return ""
	}
	return strings.TrimSpace(b.sec[sec].String())
}

func parsePlan(text string) (*Plan, []string) {
	var blocks []*block
	var cur *block
	sec := ""
	inFence, metaZone := false, false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		fence := fenceRE.MatchString(line)
		if !inFence && !fence {
			if m := blockRE.FindStringSubmatch(line); m != nil {
				cur = &block{kind: strings.ToLower(m[1]), title: m[2], meta: map[string]string{}, sec: map[string]*strings.Builder{}}
				blocks = append(blocks, cur)
				sec, metaZone = "description", cur.kind == "task"
				if cur.kind == "notes" {
					sec = "notes"
				}
				continue
			}
			if cur != nil && cur.kind != "notes" {
				if m := subRE.FindStringSubmatch(line); m != nil {
					if name := canonicalSection(cur.kind, m[1]); name != "" {
						sec, metaZone = name, false
						continue
					}
				}
				if metaZone {
					t := strings.TrimSpace(line)
					if m := metaRE.FindStringSubmatch(t); m != nil {
						key := strings.ToLower(m[1])
						if key == "depends on" {
							key = "deps"
						}
						cur.meta[key] = m[2]
						continue
					}
					if t == "" {
						continue
					}
					metaZone = false
				}
			}
		}
		if fence {
			inFence = !inFence
		}
		if cur == nil {
			continue
		}
		if strings.TrimSpace(line) != "" {
			metaZone = false
		}
		cur.add(sec, line)
	}

	p := &Plan{}
	var problems []string
	for _, b := range blocks {
		switch b.kind {
		case "epic":
			if p.Epic != nil {
				problems = append(problems, "more than one # Epic block")
				continue
			}
			p.Epic = &PlanEpic{Title: b.title, Description: b.get("description"), Design: b.get("design"),
				Acceptance: b.get("acceptance"), Decisions: b.get("decisions")}
		case "task":
			t := PlanTask{Title: b.title, Type: strings.ToLower(b.meta["type"]), Size: strings.ToUpper(b.meta["size"]),
				Description: b.get("description"), Design: b.get("design"), Acceptance: b.get("acceptance"),
				Decisions: b.get("decisions")}
			for _, tok := range regexp.MustCompile(`[,\s]+`).Split(b.meta["deps"], -1) {
				if tok = strings.Trim(tok, "()."); tok != "" && !isNone(tok) {
					t.Deps = append(t.Deps, tok)
				}
			}
			p.Tasks = append(p.Tasks, t)
		case "notes":
			p.Notes = append(p.Notes, listItems(b.get("notes"))...)
		}
	}
	if len(blocks) == 0 {
		problems = append(problems, "no '# Epic:' or '# Task:' heading found")
	}
	return p, problems
}

// Limits keep plans to contracts: Do and Check agents read the code
// themselves, and every agent of the epic re-reads this text.
const (
	maxEpicDescription = 2500
	maxEpicDesign      = 6000
	maxTaskText        = 3000
)

var taskTypes = set("task", "bug", "feature", "chore")

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// validatePlan checks a parsed plan for a mode: new or replan.
func validatePlan(p *Plan, mode string) []string {
	var problems []string
	need := func(ok bool, msg string) {
		if !ok {
			problems = append(problems, msg)
		}
	}
	switch mode {
	case "new":
		need(p.Epic != nil, "no '# Epic: <title>' block")
		if p.Epic != nil {
			e := p.Epic
			need(e.Title != "", "epic has no title")
			need(e.Description != "", "epic has no description")
			need(e.Design != "", "epic has no ## Design")
			need(e.Acceptance != "", "epic has no ## Acceptance")
			need(runeLen(e.Description) <= maxEpicDescription,
				fmt.Sprintf("epic description is %d characters (max %d)", runeLen(e.Description), maxEpicDescription))
			need(runeLen(e.Design) <= maxEpicDesign,
				fmt.Sprintf("epic design is %d characters (max %d): keep contracts, cut prose", runeLen(e.Design), maxEpicDesign))
		}
		need(len(p.Tasks) > 0, "no '# Task:' blocks")
	case "fix":
		need(len(p.Tasks) > 0, "no '# Task:' blocks for the fixes")
	case "replan":
		need(len(p.Tasks) > 0, "no replacement '# Task:' blocks")
		if p.Epic != nil {
			need(runeLen(p.Epic.Design) <= maxEpicDesign,
				fmt.Sprintf("epic design is %d characters (max %d)", runeLen(p.Epic.Design), maxEpicDesign))
		}
	}
	for i, t := range p.Tasks {
		name := fmt.Sprintf("task %d", i+1)
		if t.Title != "" {
			name = fmt.Sprintf("task %d (%s)", i+1, t.Title)
		}
		need(t.Title != "", fmt.Sprintf("task %d has no title", i+1))
		need(t.Description != "", name+" has no description")
		need(t.Acceptance != "", name+" has no ## Acceptance")
		need(t.Size == "S" || t.Size == "M" || t.Size == "L", name+" needs 'Size: S|M|L'")
		need(t.Type == "" || taskTypes[t.Type], name+": Type must be task, bug, feature or chore")
		n := runeLen(t.Description) + runeLen(t.Design)
		need(n <= maxTaskText, fmt.Sprintf("%s is %d characters (max %d): keep contracts, cut prose", name, n, maxTaskText))
		for _, d := range t.Deps {
			if m := depTokRE.FindStringSubmatch(d); m != nil {
				k := atoi(m[1])
				need(k >= 1 && k <= i, fmt.Sprintf("%s: Deps %s must name an earlier task of this file", name, d))
			}
		}
	}
	return problems
}

func countDecisions(text string) int { return len(decisRE.FindAllString(text, -1)) }

// planDecisions counts the decisions of the epic and of every task.
func planDecisions(p *Plan) int {
	n := 0
	if p.Epic != nil {
		n = countDecisions(p.Epic.Decisions)
	}
	for _, t := range p.Tasks {
		n += countDecisions(t.Decisions)
	}
	return n
}

// ---------------------------------------------------------------- review / act files

var labelRE = regexp.MustCompile(`^(?:#{1,6}\s*)?\**([A-Za-z][A-Za-z -]*?)\**\s*:\**\s*(.*)$`)

func normalizeLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "followups", "follow ups", "follow-up":
		return "follow-ups"
	case "lesson":
		return "lessons"
	case "note":
		return "notes"
	case "out-of-scope":
		return "out of scope"
	}
	return s
}

// splitLabeled splits "Label: text" sections. Only the labels in names start
// a section; other lines belong to the current one.
func splitLabeled(text string, names ...string) map[string]string {
	known := set(names...)
	res := map[string]*strings.Builder{}
	cur, inFence := "", false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		fence := fenceRE.MatchString(line)
		if !inFence && !fence && line != "" && line[0] != ' ' && line[0] != '\t' {
			if m := labelRE.FindStringSubmatch(line); m != nil {
				if name := normalizeLabel(m[1]); known[name] {
					cur = name
					if res[cur] == nil {
						res[cur] = &strings.Builder{}
					}
					if v := strings.TrimSpace(m[2]); v != "" {
						res[cur].WriteString(v + "\n")
					}
					continue
				}
			}
		}
		if fence {
			inFence = !inFence
		}
		if cur != "" {
			res[cur].WriteString(line + "\n")
		}
	}
	m := map[string]string{}
	for k, b := range res {
		m[k] = strings.TrimSpace(b.String())
	}
	return m
}

var itemRE = regexp.MustCompile(`^[-*]\s+(.*)$`)

// listItems returns the top-level "- " items of a section; indented lines
// continue the previous item. "none" items are dropped.
func listItems(sec string) []string {
	var items []string
	for _, line := range strings.Split(sec, "\n") {
		if m := itemRE.FindStringSubmatch(line); m != nil {
			items = append(items, strings.TrimSpace(m[1]))
			continue
		}
		if len(items) > 0 && strings.TrimSpace(line) != "" {
			items[len(items)-1] += "\n" + strings.TrimSpace(line)
		}
	}
	var keep []string
	for _, it := range items {
		if !isNone(it) {
			keep = append(keep, it)
		}
	}
	return keep
}

func isNone(s string) bool {
	s = strings.ToLower(strings.Trim(strings.TrimSpace(s), ".-—–*_ "))
	return s == "" || s == "none" || s == "n/a" || s == "нет"
}

var fuTypeRE = regexp.MustCompile(`(?i)^\[(bug|task|feature|chore)\]\s*(.*)$`)

// parseFollowup reads "[bug] <title> — <body>".
func parseFollowup(item string) (typ, title, body string) {
	typ = "task"
	if m := fuTypeRE.FindStringSubmatch(item); m != nil {
		typ, item = strings.ToLower(m[1]), m[2]
	}
	head, rest, _ := strings.Cut(item, "\n")
	title, tail := head, ""
	for _, sep := range []string{" — ", " – ", " -- "} {
		if a, b, ok := strings.Cut(head, sep); ok {
			title, tail = a, b
			break
		}
	}
	title = strings.TrimSpace(title)
	if r := []rune(title); len(r) > 120 {
		title = string(r[:117]) + "..."
	}
	body = strings.TrimSpace(tail + "\n" + rest)
	if body == "" {
		body = strings.TrimSpace(item)
	}
	return typ, title, body
}

// parseNoteItem reads "<task-id>: <text>".
func parseNoteItem(item string) (string, string, bool) {
	id, text, ok := strings.Cut(item, ":")
	id = strings.Trim(strings.TrimSpace(id), "`*")
	if !ok || id == "" || strings.ContainsAny(id, " \t") || strings.TrimSpace(text) == "" {
		return "", "", false
	}
	return id, strings.TrimSpace(text), true
}

var findingNumRE = regexp.MustCompile(`\bF(\d+)\b`)

// itemHead is the status part of a list item ("F4 [Important] x.py:42",
// "[NOT MET] AC2"), before the " — " that starts its explanation, so words in
// the explanation ("was NOT MET before") don't count as a status.
func itemHead(item string) string {
	head, _, _ := strings.Cut(item, "\n")
	for _, sep := range []string{" — ", " – ", " -- ", " - "} {
		if a, _, ok := strings.Cut(head, sep); ok {
			head = a
		}
	}
	return head
}

// heads returns the status part of every item of a section; a section
// without list items counts line by line.
func heads(sec string) []string {
	items := listItems(sec)
	if len(items) == 0 {
		items = strings.Split(sec, "\n")
	}
	var hs []string
	for _, it := range items {
		hs = append(hs, itemHead(it))
	}
	return hs
}

// anyHead reports whether a status word appears in the status part of an item.
func anyHead(sec, word string) bool {
	for _, h := range heads(sec) {
		if strings.Contains(strings.ToUpper(h), word) {
			return true
		}
	}
	return false
}

func countFindings(findings, outOfScope string) (c, i, m, o int) {
	for _, it := range listItems(findings) {
		// the tag belongs in the head; an item that has it only later still counts
		l := strings.ToLower(itemHead(it))
		if !strings.Contains(l, "[critical]") && !strings.Contains(l, "[important]") && !strings.Contains(l, "[minor]") {
			l = strings.ToLower(it)
		}
		switch {
		case strings.Contains(l, "[critical]"):
			c++
		case strings.Contains(l, "[important]"):
			i++
		case strings.Contains(l, "[minor]"):
			m++
		}
	}
	return c, i, m, len(listItems(outOfScope))
}

// firstWord returns the first word of s, upper-cased.
func firstWord(s string) string {
	f := strings.Fields(strings.Trim(s, "*`"))
	if len(f) == 0 {
		return ""
	}
	return strings.ToUpper(strings.Trim(f[0], "*`.,"))
}
