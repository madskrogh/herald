// Package spec checks Herald's spec against the conventions spec/README.md
// states: every implemented MUST has a vector, every vector file names
// requirements that exist, and every requirement has one ID and a status.
package spec

import (
	"bufio"
	"bytes"
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

var (
	headingRE     = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*$`)
	idRE          = regexp.MustCompile(`^R-\d+\b`)
	requirementRE = regexp.MustCompile(`^(R-\d+) · (implemented|planned|withdrawn)$`)
	mustRE        = regexp.MustCompile(`\bMUST\b`)
	delimiterRE   = regexp.MustCompile(`^\|(\s*:?-+:?\s*\|)+$`)
)

// section is the part of a file from one heading to the next.
type section struct {
	heading string // the heading's text; empty before the first heading
	line    int    // the heading's line; 0 before the first heading
	must    bool   // whether its prose says MUST
	tables  []int  // the first line of each table with at least one row
}

type requirement struct {
	id      string
	status  string
	file    string
	line    int
	must    bool
	vectors int
}

// vectorRef is a heading in a vector file that names a requirement.
type vectorRef struct {
	id   string
	file string
	line int
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
	for _, ref := range refs {
		r, ok := byID[ref.id]
		if !ok {
			problems = append(problems, Problem{File: ref.file, Line: ref.line, Message: ref.id + " names no requirement"})
			continue
		}
		r.vectors++
	}
	for _, r := range reqs {
		if r.status == "implemented" && r.must && r.vectors == 0 {
			problems = append(problems, Problem{File: r.file, Line: r.line, Message: r.id + " is an implemented MUST with no vector"})
		}
	}

	slices.SortStableFunc(problems, func(a, b Problem) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		return a.Line - b.Line
	})
	return problems, nil
}

// requirements returns the requirements in a file of the spec. A heading
// that starts with an ID but has no status is a problem.
func requirements(name string, sections []section) ([]*requirement, []Problem) {
	var (
		reqs     []*requirement
		problems []Problem
	)
	for _, s := range sections {
		if m := requirementRE.FindStringSubmatch(s.heading); m != nil {
			reqs = append(reqs, &requirement{
				id: m[1], status: m[2], file: name, line: s.line, must: s.must, vectors: len(s.tables),
			})
		} else if id := idRE.FindString(s.heading); id != "" {
			problems = append(problems, Problem{File: name, Line: s.line, Message: fmt.Sprintf(
				"%[1]s has no status: write %[2]q, %[3]q or %[4]q", id, id+" · implemented", id+" · planned", id+" · withdrawn")})
		}
	}
	return reqs, problems
}

// vectorRefs returns the requirements a vector file names. A table under a
// heading that names no requirement is a problem.
func vectorRefs(name string, sections []section) ([]vectorRef, []Problem) {
	var (
		refs     []vectorRef
		problems []Problem
	)
	for _, s := range sections {
		if len(s.tables) == 0 {
			continue
		}
		if id := idRE.FindString(s.heading); id != "" {
			refs = append(refs, vectorRef{id: id, file: name, line: s.line})
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
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	sections := []section{{}}
	cur := &sections[0]
	fenced := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if isFence(line) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if m := headingRE.FindStringSubmatch(line); m != nil {
			sections = append(sections, section{heading: m[1], line: i + 1})
			cur = &sections[len(sections)-1]
			continue
		}
		if n := tableLen(lines[i:]); n > 0 {
			if n > 2 {
				cur.tables = append(cur.tables, i+1)
			}
			i += n - 1
			continue
		}
		if mustRE.MatchString(line) {
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

func isRow(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "|")
}

// isFence reports whether line opens or closes a fenced code block.
func isFence(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}
