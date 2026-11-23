// Package spec checks Herald's spec against the conventions spec/README.md
// states: every implemented MUST has a vector, every vector file names
// requirements that exist, and every requirement has one ID, a status and a
// reason.
package spec

import (
	"cmp"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Problem is one way the spec breaks its conventions, at a line of a file.
type Problem struct {
	File    string
	Line    int
	Message string
}

func (p Problem) String() string {
	return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Message)
}

// vectorDir holds the vector files: vectors too long to sit under their
// requirement.
const vectorDir = "vectors"

// The statuses a requirement can have.
const (
	implemented = "implemented"
	planned     = "planned"
	withdrawn   = "withdrawn"
)

var statuses = []string{implemented, planned, withdrawn}

// The prefixes of the paragraphs that give a requirement's reasons.
const (
	whyPrefix       = "Why:"
	withdrawnPrefix = "Withdrawn:"
)

var (
	headingRE = regexp.MustCompile(`^ {0,3}#{1,6}\s+(.*?)\s*$`)
	// idLikeRE matches what a heading that starts with an ID starts with,
	// well-formed or not; idRE matches a well-formed ID.
	idLikeRE      = regexp.MustCompile(`^R-[0-9][^\s:·]*`)
	idRE          = regexp.MustCompile(`^R-[1-9][0-9]*$`)
	requirementRE = regexp.MustCompile(`^(R-[1-9][0-9]*) · (` + strings.Join(statuses, "|") + `)$`)
	mustRE        = regexp.MustCompile(`\bMUST\b`)
	rowRE         = regexp.MustCompile(`^ {0,3}\|`)
	delimiterRE   = regexp.MustCompile(`^\|(\s*:?-+:?\s*\|)+$`)
	fenceRE       = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
)

// section is the part of a file from one heading to the next.
type section struct {
	heading   string // the heading's text; empty before the first heading
	line      int    // the heading's line; 0 before the first heading
	must      bool   // whether its prose, other than its reasons, says MUST
	why       bool   // whether it has a "Why:" paragraph
	withdrawn bool   // whether it has a "Withdrawn:" paragraph
	tables    []int  // the first line of each table with at least one row
}

type requirement struct {
	id     string
	status string
	file   string
	line   int
	must   bool
	// why and withdrawn say whether it has a "Why:" and a "Withdrawn:"
	// paragraph.
	why, withdrawn bool
	// vectors counts its vectors: the tables under it, and those under
	// headings in vector files that name it.
	vectors int
}

// vectorRef is a heading in a vector file that names a requirement.
type vectorRef struct {
	id      string
	file    string
	line    int
	vectors int // the tables under it
}

// Check reads the spec in fsys and returns its problems, ordered by file and
// line. Files under vectors/ are vector files; every other Markdown file
// holds requirements.
func Check(fsys fs.FS) ([]Problem, error) {
	var (
		reqs     []*requirement
		refs     []vectorRef
		problems []Problem
	)
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Ext(name) != ".md" {
			return nil
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		sections := parse(data)
		if strings.HasPrefix(name, vectorDir+"/") {
			rs, ps := vectorRefs(name, sections)
			refs = append(refs, rs...)
			problems = append(problems, ps...)
		} else {
			rs, ps := requirements(name, sections)
			reqs = append(reqs, rs...)
			problems = append(problems, ps...)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}

	byID := make(map[string]*requirement, len(reqs))
	for _, r := range reqs {
		if first, ok := byID[r.id]; ok {
			problems = append(problems, Problem{File: r.file, Line: r.line, Message: fmt.Sprintf(
				"%s is already the ID of the requirement at %s:%d", r.id, first.file, first.line)})
			continue
		}
		byID[r.id] = r
	}
	// A requirement whose ID is taken has no other problems: which of the
	// two a vector belongs to is unknown until one of them is renumbered.
	reqs = slices.DeleteFunc(reqs, func(r *requirement) bool { return byID[r.id] != r })
	for _, ref := range refs {
		r, ok := byID[ref.id]
		if !ok {
			problems = append(problems, Problem{File: ref.file, Line: ref.line, Message: ref.id + " names no requirement"})
			continue
		}
		r.vectors += ref.vectors
	}
	for _, r := range reqs {
		if !r.why {
			problems = append(problems, Problem{File: r.file, Line: r.line, Message: fmt.Sprintf(
				"%s has no reason: add a %q line", r.id, whyPrefix)})
		}
		if r.status == withdrawn && !r.withdrawn {
			problems = append(problems, Problem{File: r.file, Line: r.line, Message: fmt.Sprintf(
				"%s is withdrawn with no reason: add a %q line", r.id, withdrawnPrefix)})
		}
		if r.status == implemented && r.must && r.vectors == 0 {
			problems = append(problems, Problem{File: r.file, Line: r.line, Message: r.id + " is an implemented MUST with no vector"})
		}
	}

	slices.SortStableFunc(problems, func(a, b Problem) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		return cmp.Compare(a.Line, b.Line)
	})
	return problems, nil
}

// headingID returns the ID a heading starts with, or "" if it starts with
// none. A malformed ID is a problem.
func headingID(name string, s section) (string, *Problem) {
	id := idLikeRE.FindString(s.heading)
	if id == "" || idRE.MatchString(id) {
		return id, nil
	}
	return "", &Problem{File: name, Line: s.line, Message: fmt.Sprintf(
		`%q is not an ID: write R- and a number with no leading zero, such as "R-1"`, id)}
}

// requirements returns the requirements in a file of the spec. A heading
// that starts with an ID but has no status is a problem.
func requirements(name string, sections []section) ([]*requirement, []Problem) {
	var (
		reqs     []*requirement
		problems []Problem
	)
	for _, s := range sections {
		id, p := headingID(name, s)
		if p != nil {
			problems = append(problems, *p)
		}
		if id == "" {
			continue
		}
		m := requirementRE.FindStringSubmatch(s.heading)
		if m == nil {
			problems = append(problems, Problem{File: name, Line: s.line, Message: noStatus(id)})
			continue
		}
		reqs = append(reqs, &requirement{
			id: m[1], status: m[2], file: name, line: s.line,
			must: s.must, why: s.why, withdrawn: s.withdrawn, vectors: len(s.tables),
		})
	}
	return reqs, problems
}

// noStatus says how to write the heading of requirement id, which has no
// status.
func noStatus(id string) string {
	headings := make([]string, len(statuses))
	for i, s := range statuses {
		headings[i] = fmt.Sprintf("%q", id+" · "+s)
	}
	last := len(headings) - 1
	return fmt.Sprintf("%s has no status: write %s or %s", id, strings.Join(headings[:last], ", "), headings[last])
}

// vectorRefs returns the requirements a vector file names. A table under a
// heading that names no requirement is a problem.
func vectorRefs(name string, sections []section) ([]vectorRef, []Problem) {
	var (
		refs     []vectorRef
		problems []Problem
	)
	for _, s := range sections {
		id, p := headingID(name, s)
		if p != nil {
			problems = append(problems, *p)
			continue
		}
		if id != "" {
			refs = append(refs, vectorRef{id: id, file: name, line: s.line, vectors: len(s.tables)})
			continue
		}
		for _, line := range s.tables {
			problems = append(problems, Problem{File: name, Line: line, Message: `vector is under no requirement: put it under a heading that starts with the requirement's ID, such as "## R-1"`})
		}
	}
	return refs, problems
}

// parse splits a Markdown file into sections. Fenced code blocks, such as an
// ABNF grammar or an example, are skipped.
func parse(data []byte) []section {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")

	sections := []section{{}}
	cur := &sections[0]
	fence := ""     // the fence that opened the code block being skipped
	reason := false // whether the line is in a paragraph giving a reason
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if fence != "" {
			if closes(line, fence) {
				fence = ""
			}
			continue
		}
		if fence = opens(line); fence != "" {
			continue
		}
		if m := headingRE.FindStringSubmatch(line); m != nil {
			sections = append(sections, section{heading: m[1], line: i + 1})
			cur = &sections[len(sections)-1]
			reason = false
			continue
		}
		if n := tableLen(lines[i:]); n > 0 {
			if n > 2 {
				cur.tables = append(cur.tables, i+1)
			}
			i += n - 1
			reason = false
			continue
		}
		switch t := strings.TrimSpace(line); {
		case t == "":
			reason = false
		case strings.HasPrefix(t, whyPrefix):
			cur.why, reason = true, true
		case strings.HasPrefix(t, withdrawnPrefix):
			cur.withdrawn, reason = true, true
		}
		if !reason && mustRE.MatchString(line) {
			cur.must = true
		}
	}
	return sections
}

// tableLen returns the number of lines of the Markdown table that starts
// lines, counting its header and delimiter rows, or 0 if none starts there.
func tableLen(lines []string) int {
	if len(lines) < 2 || !isRow(lines[0]) || !delimiterRE.MatchString(strings.TrimSpace(lines[1])) {
		return 0
	}
	n := 2
	for n < len(lines) && isRow(lines[n]) {
		n++
	}
	return n
}

// isRow reports whether line is a table row. A line indented by four
// spaces or more is code, not a row.
func isRow(line string) bool {
	return rowRE.MatchString(line)
}

// opens returns the fence that opens a fenced code block on line, or "" if
// line opens none. Like CommonMark, a backtick fence's info string can't
// contain a backtick, so a line that starts with inline code opens nothing.
func opens(line string) string {
	m := fenceRE.FindStringSubmatch(line)
	if m == nil || (m[1][0] == '`' && strings.Contains(m[2], "`")) {
		return ""
	}
	return m[1]
}

// closes reports whether line closes the code block that fence opened: a
// fence of the same character, at least as long, with nothing after it.
func closes(line, fence string) bool {
	m := fenceRE.FindStringSubmatch(line)
	return m != nil && m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(m[2]) == ""
}
