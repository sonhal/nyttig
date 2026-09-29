package client

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// proxySubtestEnv marks the re-executed child process in
// TestClient_UnixSocketIgnoresProxy.
const proxySubtestEnv = "NYTTIG_PROXY_SUBTEST"

// TestClient_UnixSocketIgnoresProxy checks that a client dialing the daemon's
// Unix socket connects directly even when HTTPS_PROXY is set. gRPC applies
// the proxy environment to every target, and a proxy cannot reach a local
// socket, so without the fix every RPC fails with a proxy handshake error.
//
// net/http reads the proxy environment once per process and caches it, so
// setting it with t.Setenv here would be order-dependent. Instead the test
// re-runs itself in a child process with the proxy variables set from start.
func TestClient_UnixSocketIgnoresProxy(t *testing.T) {
	if os.Getenv(proxySubtestEnv) == "1" {
		dialUnixSocketThroughClient(t)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestClient_UnixSocketIgnoresProxy$", "-test.v")
	// Port 1 on loopback refuses connections, so any attempt to use the proxy
	// fails fast.
	cmd.Env = append(os.Environ(),
		proxySubtestEnv+"=1",
		"HTTPS_PROXY=http://127.0.0.1:1", "https_proxy=http://127.0.0.1:1",
		"HTTP_PROXY=http://127.0.0.1:1", "http_proxy=http://127.0.0.1:1",
		"NO_PROXY=", "no_proxy=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("client could not reach Unix socket with a proxy configured: %v\n%s", err, out)
	}
}

func dialUnixSocketThroughClient(t *testing.T) {
	// Keep the socket path short: sun_path is limited to ~108 bytes and
	// t.TempDir() can exceed that.
	dir, err := os.MkdirTemp("", "nyttig")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "d.sock")

	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	pb.RegisterNyttigServer(srv, newTestServer())
	go srv.Serve(lis)
	defer srv.Stop()

	c := New(Options{Addr: sock})
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.ListSources(ctx); err != nil {
		t.Fatalf("ListSources over %s: %v", sock, err)
	}
}
