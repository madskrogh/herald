package egress_test

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/madskrogh/herald/internal/egress"
)

func TestCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		addr string
		// want is a word the refusal must contain, or empty if addr is public.
		want string
	}{
		{addr: "8.8.8.8"},
		{addr: "1.1.1.1"},
		{addr: "93.184.215.14"},
		{addr: "2606:4700:4700::1111"},
		{addr: "2a00:1450:4001:80b::200e"},

		{addr: "127.0.0.1", want: "loopback"},
		{addr: "127.255.255.254", want: "loopback"},
		{addr: "::1", want: "loopback"},
		{addr: "::ffff:127.0.0.1", want: "loopback"},
		{addr: "10.0.0.1", want: "private"},
		{addr: "172.16.0.1", want: "private"},
		{addr: "172.31.255.255", want: "private"},
		{addr: "192.168.1.1", want: "private"},
		{addr: "::ffff:192.168.1.1", want: "private"},
		{addr: "fd00::1", want: "private"},
		{addr: "fc00::1", want: "private"},
		{addr: "169.254.169.254", want: "link-local"},
		{addr: "fe80::1", want: "link-local"},
		{addr: "fe80::1%en0", want: "link-local"},
		{addr: "0.0.0.0", want: "unspecified"},
		{addr: "::", want: "unspecified"},
		{addr: "224.0.0.1", want: "multicast"},
		{addr: "239.255.255.250", want: "multicast"},
		{addr: "ff02::1", want: "multicast"},
		{addr: "255.255.255.255", want: "not public"},
		{addr: "0.1.2.3", want: "not public"},
		{addr: "100.64.0.1", want: "not public"},
		{addr: "100.127.255.255", want: "not public"},
		{addr: "192.0.0.8", want: "not public"},
		{addr: "192.0.2.1", want: "not public"},
		{addr: "192.88.99.1", want: "not public"},
		{addr: "198.18.0.1", want: "not public"},
		{addr: "198.51.100.1", want: "not public"},
		{addr: "203.0.113.7", want: "not public"},
		{addr: "240.0.0.1", want: "not public"},
		{addr: "2001:db8::1", want: "not public"},
		{addr: "100::1", want: "not public"},
		{addr: "64:ff9b::7f00:1", want: "loopback"},
		{addr: "64:ff9b::a9fe:a9fe", want: "link-local"},
		{addr: "64:ff9b:1::1", want: "not public"},
		{addr: "2002:7f00:1::1", want: "loopback"},
		{addr: "2002:c0a8:101::1", want: "private"},
		{addr: "2001::1", want: "not public"},
		{addr: "::127.0.0.1", want: "not public"},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()
			err := egress.Check(netip.MustParseAddr(tt.addr))
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("Check = %v, want public", err)
			case tt.want != "" && err == nil:
				t.Errorf("Check accepted it, want a refusal saying %q", tt.want)
			case tt.want != "" && !strings.Contains(err.Error(), tt.want):
				t.Errorf("Check = %v, want it to say %q", err, tt.want)
			}
		})
	}
}

func TestCheckRefusesTheZeroAddr(t *testing.T) {
	t.Parallel()
	if egress.Check(netip.Addr{}) == nil {
		t.Error("Check accepted the zero Addr")
	}
}

// FuzzCheckEmbeddedIPv4 checks that an IPv4 address gets the same verdict
// in each IPv6 form that reaches it.
func FuzzCheckEmbeddedIPv4(f *testing.F) {
	for _, s := range []string{"8.8.8.8", "127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.0.1", "0.0.0.0"} {
		f.Add(netip.MustParseAddr(s).AsSlice())
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) != 4 {
			return
		}
		v4 := netip.AddrFrom4([4]byte(b))
		var nat64, sixTo4 [16]byte
		copy(nat64[:], []byte{0x00, 0x64, 0xff, 0x9b})
		copy(nat64[12:], b)
		copy(sixTo4[:], []byte{0x20, 0x02})
		copy(sixTo4[2:], b)
		want := egress.Check(v4) == nil
		for _, a := range []netip.Addr{netip.AddrFrom16(v4.As16()), netip.AddrFrom16(nat64), netip.AddrFrom16(sixTo4)} {
			if got := egress.Check(a) == nil; got != want {
				t.Errorf("Check(%s) public = %t, but Check(%s) public = %t", a, got, v4, want)
			}
		}
	})
}
