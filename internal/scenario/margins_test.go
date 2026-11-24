package scenario

import (
	"os"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/spec"
)

// TestMarginsFollowTheDocs compares each margin of the behaviour
// comparison with its row in docs/testing.md, "Timing margins", where its
// reason is, so the comparison can't drift from what the docs say.
func TestMarginsFollowTheDocs(t *testing.T) {
	t.Parallel()
	tables, err := spec.FileTables(os.DirFS("../../docs"), "testing.md")
	if err != nil {
		t.Fatal(err)
	}
	tb, ok := spec.TableUnder(tables, "Timing margins")
	if !ok {
		t.Fatal(`docs/testing.md has no table under "Timing margins"`)
	}
	records, err := tb.Records()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, r := range records {
		want[r.Cells["Statistic"]] = r.Cells["Margin"]
	}
	format := func(m Margin) string {
		return strconv.FormatInt(m.Percent, 10) + "% + " + strconv.FormatInt(m.Ms, 10) + " ms"
	}
	got := map[string]string{edgeStatistic: format(edgeMargin)}
	for name, m := range TimingMargins {
		got[name] = format(m)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("the comparison's margins differ from docs/testing.md (-docs +code):\n%s", diff)
	}
}
