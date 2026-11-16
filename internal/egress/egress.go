// Package egress decides which addresses herald-dispatcher may connect to.
// Endpoint URLs are Tenant input, so without this an Endpoint such as
// https://169.254.169.254/ would make herald-dispatcher a proxy into Herald's
// own network. The check is made on the address actually dialled, after DNS
// resolution, because a public host name can resolve to a private address
// later (DNS rebinding).
package egress

import (
	"errors"
	"fmt"
	"net/netip"
	"syscall"
)

// ipv6GlobalUnicast is the IPv6 global unicast range. No IPv6 address
// outside it is public.
var ipv6GlobalUnicast = netip.MustParsePrefix("2000::/3")

// notPublic are ranges that the named classes in Check don't cover and that
// the IANA special-purpose address registries list as not globally
// reachable, or reserved for documentation or benchmarking.
var notPublic = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // shared address space (carrier-grade NAT)
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation (TEST-NET-1)
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation (TEST-NET-2)
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation (TEST-NET-3)
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, and the limited broadcast address
	netip.MustParsePrefix("2001::/23"),       // IETF protocol assignments, including Teredo
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("3fff::/20"),       // documentation
}

// Prefixes whose addresses carry an IPv4 address in the given bytes: NAT64
// (RFC 6052) in the last four, 6to4 (RFC 3056) in bytes 2 to 5. A gateway
// would deliver to the IPv4 address, so that is the one checked.
var (
	nat64  = netip.MustParsePrefix("64:ff9b::/96")
	sixTo4 = netip.MustParsePrefix("2002::/16")
)

// Control is a net.Dialer Control function that refuses to connect to an
// address that is not public. It runs after DNS resolution, on every address
// the dialer tries, so it sees the address actually dialled.
func Control(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("parse dialled address %q: %w", address, err)
	}
	return Check(ap.Addr())
}

// Check returns an error saying why addr is not a public address, or nil if
// it is one.
func Check(addr netip.Addr) error {
	if !addr.IsValid() {
		return errors.New("invalid address")
	}
	// An IPv4-mapped IPv6 address reaches the IPv4 address it maps.
	addr = addr.Unmap().WithZone("")

	if b := addr.As16(); addr.Is6() && (nat64.Contains(addr) || sixTo4.Contains(addr)) {
		embedded := netip.AddrFrom4([4]byte(b[12:16]))
		if sixTo4.Contains(addr) {
			embedded = netip.AddrFrom4([4]byte(b[2:6]))
		}
		if err := Check(embedded); err != nil {
			return fmt.Errorf("address %s embeds %s: %w", addr, embedded, err)
		}
		return nil
	}

	var class string
	switch {
	case addr.IsUnspecified():
		class = "unspecified"
	case addr.IsLoopback():
		class = "loopback"
	case addr.IsPrivate():
		class = "private"
	case addr.IsLinkLocalUnicast():
		class = "link-local"
	case addr.IsMulticast():
		class = "multicast"
	}
	if class != "" {
		return fmt.Errorf("address %s is %s, not public", addr, class)
	}
	if addr.Is6() && !ipv6GlobalUnicast.Contains(addr) {
		return fmt.Errorf("address %s is not public", addr)
	}
	for _, p := range notPublic {
		if p.Contains(addr) {
			return fmt.Errorf("address %s is not public", addr)
		}
	}
	return nil
}
