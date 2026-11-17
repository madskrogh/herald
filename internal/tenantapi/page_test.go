package tenantapi

import (
	"testing"
	"uuid"
)

func FuzzParsePage(f *testing.F) {
	f.Add(int32(0), "")
	f.Add(int32(2), "AZOhsgAAcACAAAAAAAAAAA")
	f.Add(int32(-1), "!!!")
	f.Add(int32(1000), "AAAA")
	f.Fuzz(func(t *testing.T, size int32, token string) {
		p, err := parsePage(size, token)
		if err != nil {
			return
		}
		if p.size < 1 || p.size > 100 {
			t.Errorf("parsePage(%d, %q) has size %d, want 1 to 100", size, token, p.size)
		}
	})
}

func FuzzPageTokenRoundTrip(f *testing.F) {
	f.Add([]byte("0123456789abcdef"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) != 16 {
			return
		}
		last := uuid.UUID(b)
		// A full page and one more, so cut makes a token for the next page.
		p := page{size: 1}
		_, token := cut(p, []uuid.UUID{last, uuid.Max()}, func(id uuid.UUID) uuid.UUID { return id })
		next, err := parsePage(0, token)
		if err != nil {
			t.Fatalf("parsePage of the token cut made: %v", err)
		}
		if next.after != last {
			t.Errorf("token %q continues after %s, want %s", token, next.after, last)
		}
	})
}
