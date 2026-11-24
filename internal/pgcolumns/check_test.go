package pgcolumns_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/madskrogh/herald/internal/pgcolumns"
)

// TestCheck shows each way a database can break R-84 and R-85, against a
// list of three columns: one the scenario writes, one Postgres generates
// when it does, and one Herald writes that may be NULL.
func TestCheck(t *testing.T) {
	t.Parallel()

	list := []pgcolumns.Column{
		{Table: "ingest.tenants", Name: "id", Type: "uuid", SetBy: pgcolumns.SetByDatabase},
		{Table: "ingest.tenants", Name: "name", Type: "text", SetBy: pgcolumns.SetByScenario},
		{Table: "ingest.messages", Name: "handed_off_at", Type: "timestamp with time zone", Nullable: true, SetBy: pgcolumns.SetByHerald},
	}
	// matching is a database that keeps the list, with columns it doesn't
	// name: one with a default in a table the scenario writes, and one
	// NOT NULL with no default in a table it only reads.
	matching := func() []pgcolumns.Stored {
		return []pgcolumns.Stored{
			{Table: "ingest.tenants", Name: "id", Type: "uuid", HasDefault: true},
			{Table: "ingest.tenants", Name: "name", Type: "text"},
			{Table: "ingest.tenants", Name: "created_at", Type: "timestamp with time zone", HasDefault: true},
			{Table: "ingest.messages", Name: "handed_off_at", Type: "timestamp with time zone", Nullable: true},
			{Table: "ingest.messages", Name: "payload", Type: "bytea"},
		}
	}

	tests := []struct {
		name   string
		change func([]pgcolumns.Stored) []pgcolumns.Stored
		want   []string
	}{
		{
			name:   "a database that keeps the list",
			change: func(s []pgcolumns.Stored) []pgcolumns.Stored { return s },
		},
		{
			name: "a listed column is missing",
			change: func(s []pgcolumns.Stored) []pgcolumns.Stored {
				return append(s[:3], s[4:]...)
			},
			want: []string{"ingest.messages.handed_off_at is missing"},
		},
		{
			name: "a listed column has another type",
			change: func(s []pgcolumns.Stored) []pgcolumns.Stored {
				s[1].Type = "character varying(100)"
				return s
			},
			want: []string{`ingest.tenants.name is character varying(100), want text`},
		},
		{
			name: "a column the list says is never NULL may be",
			change: func(s []pgcolumns.Stored) []pgcolumns.Stored {
				s[1].Nullable = true
				return s
			},
			want: []string{"ingest.tenants.name may be NULL, but the list says never"},
		},
		{
			name: "a column the list says may be NULL never is",
			change: func(s []pgcolumns.Stored) []pgcolumns.Stored {
				s[3].Nullable = false
				return s
			},
		},
		{
			name: "a column the database sets has no default",
			change: func(s []pgcolumns.Stored) []pgcolumns.Stored {
				s[0].HasDefault = false
				return s
			},
			want: []string{"ingest.tenants.id has no default, but the list says the database sets it"},
		},
		{
			name: "a column the list doesn't name, in a table the scenario writes, has no default and isn't NULL",
			change: func(s []pgcolumns.Stored) []pgcolumns.Stored {
				s[2].HasDefault = false
				return s
			},
			want: []string{"ingest.tenants.created_at is NOT NULL with no default, so the scenario can't write a row of ingest.tenants"},
		},
		{
			name: "a column the list doesn't name, in a table the scenario writes, may be NULL",
			change: func(s []pgcolumns.Stored) []pgcolumns.Stored {
				s[2].HasDefault, s[2].Nullable = false, true
				return s
			},
		},
	}
	t.Run("Unlisted names the columns the list doesn't", func(t *testing.T) {
		t.Parallel()
		var got []string
		for _, s := range pgcolumns.Unlisted(list, matching()) {
			got = append(got, s.Table+"."+s.Name)
		}
		want := []string{"ingest.tenants.created_at", "ingest.messages.payload"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Unlisted (-want +got):\n%s", diff)
		}
	})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, err := range pgcolumns.Check(list, tt.change(matching())) {
				got = append(got, err.Error())
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Check (-want +got):\n%s", diff)
			}
		})
	}
}
