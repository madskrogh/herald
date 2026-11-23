package scenario

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// ruleGoroutines is the rule CompareGoroutines checks.
const ruleGoroutines = "goroutines"

// Goroutines is a service's goroutine profile: how many goroutines have
// each stack. A stack is its functions, innermost first, one per line,
// without addresses or offsets, so it compares across builds and restarts.
type Goroutines map[string]int

// noFrames is the stack of goroutines whose frames a profile doesn't show,
// as for one that is running.
const noFrames = "(no frames)"

// ParseGoroutines parses a goroutine profile in pprof's debug=1 text, as
// /debug/pprof/goroutine?debug=1 serves it. Each record is a count and the
// stack's addresses, then a line for each frame:
//
//	2 @ 0x200e4 0x20a2c
//	#	0x200e3	internal/poll.runtime_pollWait+0x9f	/usr/local/go/src/runtime/netpoll.go:351
func ParseGoroutines(profile []byte) (Goroutines, error) {
	sc := bufio.NewScanner(bytes.NewReader(profile))
	if !sc.Scan() || !strings.HasPrefix(sc.Text(), "goroutine profile:") {
		return nil, errors.New("not a goroutine profile in debug=1 text")
	}
	g := Goroutines{}
	count := 0
	var frames []string
	// flush counts the record read so far, if any.
	flush := func() {
		if count == 0 {
			return
		}
		stack := strings.Join(frames, "\n")
		if len(frames) == 0 {
			stack = noFrames
		}
		g[stack] += count
		count, frames = 0, nil
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "#\t"):
			// "#", the address, the function with its offset, and the file,
			// each column padded with tabs.
			fields := strings.Fields(line)
			if count == 0 || len(fields) < 3 {
				return nil, fmt.Errorf("frame %q outside a record", line)
			}
			fn, _, _ := strings.Cut(fields[2], "+")
			frames = append(frames, fn)
		case strings.HasPrefix(line, "#"):
			// A record's labels.
		default:
			flush()
			n, _, ok := strings.Cut(line, " @ ")
			c, err := strconv.Atoi(n)
			if !ok || err != nil || c <= 0 {
				return nil, fmt.Errorf("record header %q isn't a count and addresses", line)
			}
			count = c
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flush()
	if len(g) == 0 {
		return nil, errors.New("a goroutine profile with no goroutines")
	}
	return g, nil
}

// CompareGoroutines checks that a service's goroutines after a run are back
// to its idle set: no stack has more goroutines than before the run. Each
// stack with more is a leak, reported with how many more and its
// functions. A missing profile fails the check.
func CompareGoroutines(service string, idle, after Goroutines) []Violation {
	if len(idle) == 0 || len(after) == 0 {
		return []Violation{{Rule: ruleGoroutines, Subject: service, Detail: "nothing to check: a goroutine profile is empty"}}
	}
	var vs []Violation
	for _, stack := range slices.Sorted(maps.Keys(after)) {
		if more := after[stack] - idle[stack]; more > 0 {
			noun := "goroutines"
			if more == 1 {
				noun = "goroutine"
			}
			vs = append(vs, Violation{
				Rule:    ruleGoroutines,
				Subject: service,
				Detail:  fmt.Sprintf("%d more %s than idle in\n\t%s", more, noun, strings.ReplaceAll(stack, "\n", "\n\t")),
			})
		}
	}
	return vs
}
