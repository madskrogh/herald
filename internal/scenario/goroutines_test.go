package scenario_test

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/scenario"
)

// idleProfile is a goroutine profile of an idle service, in pprof's
// debug=1 text: one goroutine serving the profile, and two waiting for
// connections. pprof aligns a frame's columns with tabs, more for shorter
// functions.
const idleProfile = `goroutine profile: total 3
1 @ 0x100e4 0x10a2c 0x10b14
#	0x100e3	runtime/pprof.writeRuntimeProfile+0xb3	/usr/local/go/src/runtime/pprof/pprof.go:796
#	0x10a2b	net/http/pprof.handler.ServeHTTP+0x4ab	/usr/local/go/src/net/http/pprof/pprof.go:272
#	0x10b13	net/http.(*conn).serve+0x123	/usr/local/go/src/net/http/server.go:2102

2 @ 0x200e4 0x20a2c
#	0x200e3	internal/poll.runtime_pollWait+0x9f	/usr/local/go/src/runtime/netpoll.go:351
#	0x20a2b		net.(*TCPListener).Accept+0x2b		/usr/local/go/src/net/tcpsock.go:380
`

// leakedProfile is idleProfile with a third goroutine waiting for a
// connection, and one stuck sending on a channel. Addresses and offsets
// differ, as in another build of the same code.
const leakedProfile = `goroutine profile: total 5
1 @ 0x100f4 0x10a3c 0x10b24
#	0x100f3	runtime/pprof.writeRuntimeProfile+0xc3	/usr/local/go/src/runtime/pprof/pprof.go:796
#	0x10a3b	net/http/pprof.handler.ServeHTTP+0x4bb	/usr/local/go/src/net/http/pprof/pprof.go:272
#	0x10b23	net/http.(*conn).serve+0x133	/usr/local/go/src/net/http/server.go:2102

3 @ 0x200f4 0x20a3c
#	0x200f3	internal/poll.runtime_pollWait+0xaf	/usr/local/go/src/runtime/netpoll.go:351
#	0x20a3b		net.(*TCPListener).Accept+0x3b			/usr/local/go/src/net/tcpsock.go:380

1 @ 0x300e4 0x30a2c
# labels: {"worker":"7"}
#	0x300e3	runtime.chansend1+0x1f	/usr/local/go/src/runtime/chan.go:161
#	0x30a2b	github.com/madskrogh/herald/internal/delivery.(*Workers).attempt+0x2b	/src/internal/delivery/workers.go:300
`

func TestCompareGoroutinesPassesWhenTheyReturnToTheIdleSet(t *testing.T) {
	t.Parallel()
	idle := parseGoroutines(t, idleProfile)
	// Fewer goroutines than when idle is fine: a restarted service starts
	// with fewer.
	after := parseGoroutines(t, strings.Replace(idleProfile, "2 @ 0x200e4", "1 @ 0x200e4", 1))
	for _, profile := range []scenario.Goroutines{idle, after} {
		if got := scenario.CompareGoroutines("herald-api", idle, profile); len(got) != 0 {
			t.Errorf("CompareGoroutines = %v, want no violations", got)
		}
	}
}

// TestCompareGoroutinesNamesTheLeakedStack checks that each stack with more
// goroutines than when idle is reported, with how many more and the stack's
// functions, innermost first.
func TestCompareGoroutinesNamesTheLeakedStack(t *testing.T) {
	t.Parallel()
	got := scenario.CompareGoroutines("herald-dispatcher", parseGoroutines(t, idleProfile), parseGoroutines(t, leakedProfile))
	want := []scenario.Violation{
		{
			Rule:    "goroutines",
			Subject: "herald-dispatcher",
			Detail:  "1 more goroutine than idle in\n\tinternal/poll.runtime_pollWait\n\tnet.(*TCPListener).Accept",
		},
		{
			Rule:    "goroutines",
			Subject: "herald-dispatcher",
			Detail:  "1 more goroutine than idle in\n\truntime.chansend1\n\tgithub.com/madskrogh/herald/internal/delivery.(*Workers).attempt",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("CompareGoroutines (-want +got):\n%s", diff)
	}
}

// TestParseGoroutinesCountsAGoroutineWithoutFrames checks that a goroutine
// whose stack the profile doesn't show, as for one that is running, still
// counts.
func TestParseGoroutinesCountsAGoroutineWithoutFrames(t *testing.T) {
	t.Parallel()
	got := parseGoroutines(t, "goroutine profile: total 2\n1 @ 0x1\n\n"+idleProfile[len("goroutine profile: total 3\n"):])
	if n := got["(no frames)"]; n != 1 {
		t.Errorf("goroutines without frames = %d, want 1", n)
	}
}

func TestCompareGoroutinesFailsOnAnEmptyProfile(t *testing.T) {
	t.Parallel()
	idle := parseGoroutines(t, idleProfile)
	for _, tt := range []struct {
		name        string
		idle, after scenario.Goroutines
	}{
		{"no idle profile", nil, idle},
		{"no profile after the run", idle, nil},
	} {
		if got := scenario.CompareGoroutines("herald-api", tt.idle, tt.after); len(got) == 0 {
			t.Errorf("%s: CompareGoroutines = no violations, want one", tt.name)
		}
	}
}

func TestParseGoroutinesRejectsWhatIsNoProfile(t *testing.T) {
	t.Parallel()
	for _, profile := range []string{
		"",
		"404 page not found\n",
		"goroutine profile: total 1\nx @ 0x1\n#\t0x1\tmain.main+0x1\t/main.go:1\n",
		"goroutine profile: total 1\n#\t0x1\tmain.main+0x1\t/main.go:1\n",
	} {
		if g, err := scenario.ParseGoroutines([]byte(profile)); err == nil {
			t.Errorf("ParseGoroutines(%q) = %v, want an error", profile, g)
		}
	}
}

func parseGoroutines(t *testing.T, profile string) scenario.Goroutines {
	t.Helper()
	g, err := scenario.ParseGoroutines([]byte(profile))
	if err != nil {
		t.Fatalf("ParseGoroutines: %v", err)
	}
	return g
}
