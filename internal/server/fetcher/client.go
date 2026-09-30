package fetcher

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ClientOptions configures the HTTP client used to fetch feeds.
type ClientOptions struct {
	// BlockPrivateAddresses refuses connections to loopback, private,
	// link-local and other non-public addresses. Enable it when untrusted
	// users can add sources (for example through a web client), so a feed
	// URL cannot be used to reach internal services (SSRF).
	//
	// The check runs on the resolved IP at connect time, so it also covers
	// DNS names that resolve to internal addresses, DNS rebinding and
	// redirects. HTTP proxies from the environment are ignored while it is
	// enabled: through a proxy the real destination is invisible to the
	// check.
	BlockPrivateAddresses bool
}

// ErrBlockedAddress is returned (wrapped) when a fetch is refused because
// the destination is not a public address.
var ErrBlockedAddress = errors.New("destination address is not public")

// NewHTTPClient returns the HTTP client for feed fetching: a 30s overall
// timeout plus, optionally, private-address blocking.
func NewHTTPClient(opts ClientOptions) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if opts.BlockPrivateAddresses {
		dialer.Control = blockNonPublic
		transport.Proxy = nil
	}
	transport.DialContext = dialer.DialContext
	return &http.Client{Timeout: 30 * time.Second, Transport: transport}
}

// blockNonPublic is a net.Dialer Control hook. address is the resolved
// "ip:port" about to be connected to.
func blockNonPublic(network, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: cannot parse %q: %v", ErrBlockedAddress, address, err)
	}
	if !isPublicAddr(ap.Addr()) {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, ap.Addr())
	}
	return nil
}

// nonPublicPrefixes are ranges netip's Is* helpers do not cover.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // carrier-grade NAT (also Tailscale)
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, incl. broadcast
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64: embeds an IPv4 address
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
}

// isPublicAddr reports whether addr is a globally routable unicast address.
func isPublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap() // treat ::ffff:10.0.0.1 as 10.0.0.1
	if !addr.IsValid() ||
		addr.IsLoopback() ||
		addr.IsPrivate() || // 10/8, 172.16/12, 192.168/16, fc00::/7
		addr.IsLinkLocalUnicast() || // 169.254/16 (cloud metadata), fe80::/10
		addr.IsUnspecified() ||
		addr.IsMulticast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}
