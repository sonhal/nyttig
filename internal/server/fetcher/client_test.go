package fetcher

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"
)

func TestIsPublicAddr(t *testing.T) {
	tests := []struct {
		addr   string
		public bool
	}{
		{"93.184.215.14", true},
		{"1.1.1.1", true},
		{"2606:4700:4700::1111", true},

		{"127.0.0.1", false},
		{"127.10.0.1", false},
		{"::1", false},
		{"10.1.2.3", false},
		{"172.16.0.1", false},
		{"172.31.255.255", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false}, // cloud metadata
		{"fe80::1", false},
		{"fd00::1", false},
		{"0.0.0.0", false},
		{"0.1.2.3", false},
		{"::", false},
		{"100.64.0.1", false}, // CGNAT / Tailscale
		{"100.127.255.254", false},
		{"198.18.0.1", false},
		{"224.0.0.1", false},
		{"255.255.255.255", false},
		{"::ffff:127.0.0.1", false}, // IPv4-mapped loopback
		{"::ffff:10.0.0.1", false},
		{"64:ff9b::a9fe:a9fe", false}, // NAT64 of 169.254.169.254
		{"2001:db8::1", false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := isPublicAddr(netip.MustParseAddr(tt.addr)); got != tt.public {
				t.Errorf("isPublicAddr(%s) = %v, want %v", tt.addr, got, tt.public)
			}
		})
	}
}

func TestNewHTTPClient_BlocksPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// httptest listens on 127.0.0.1, which the blocking client must refuse.
	blocking := NewHTTPClient(ClientOptions{BlockPrivateAddresses: true})
	resp, err := blocking.Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected blocked request to loopback")
	}
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("error = %v, want ErrBlockedAddress", err)
	}

	// The same request is allowed when blocking is off.
	open := NewHTTPClient(ClientOptions{})
	resp, err = open.Get(srv.URL)
	if err != nil {
		t.Fatalf("unblocked request: %v", err)
	}
	_ = resp.Body.Close()
}

func TestNewHTTPClient_BlocksHostnameResolvingToPrivate(t *testing.T) {
	// The check runs on the resolved IP, so a hostname that points at an
	// internal address is refused like the literal IP (this is what defeats
	// DNS-based tricks and redirects to internal names).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	port := netip.MustParseAddrPort(srv.Listener.Addr().String()).Port()
	url := "http://localhost:" + strconv.Itoa(int(port)) + "/"

	client := NewHTTPClient(ClientOptions{BlockPrivateAddresses: true})
	resp, err := client.Get(url)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected request to localhost to be blocked")
	}
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("error = %v, want ErrBlockedAddress", err)
	}
}

func TestNewHTTPClient_BlocksRedirectToPrivate(t *testing.T) {
	// Every connection is dialed through the same check, including the ones
	// made to follow a redirect. The first hop is simulated with a transport
	// that returns a redirect without dialing, so only the second hop, to a
	// loopback server, reaches the dialer.
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer internal.Close()

	client := NewHTTPClient(ClientOptions{BlockPrivateAddresses: true})
	real := client.Transport
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "feed.example" {
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": {internal.URL + "/"}},
				Body:       http.NoBody,
				Request:    req,
			}, nil
		}
		return real.RoundTrip(req)
	})

	resp, err := client.Get("http://feed.example/rss")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected redirect to loopback to be blocked")
	}
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("error = %v, want ErrBlockedAddress", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
