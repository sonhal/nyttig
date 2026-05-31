package mtls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// certAuthority is a tiny in-test CA used to mint server and client certs.
type certAuthority struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
}

func newCA(t *testing.T) *certAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ca key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "nyttig-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("ca cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse ca: %v", err)
	}
	return &certAuthority{
		cert:    cert,
		key:     key,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

// issue mints a leaf certificate signed by the CA and returns its cert+key PEM.
// serverName, when non-empty, is added as a DNS SAN; 127.0.0.1 is always added
// as an IP SAN so tests can dial over loopback.
func (ca *certAuthority) issue(t *testing.T, cn, serverName string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	if serverName != "" {
		tmpl.DNSNames = []string{serverName}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("leaf cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// testEnv writes a CA, a server keypair, and a client keypair to a temp dir and
// returns the file paths.
type testEnv struct {
	caFile     string
	serverCert string
	serverKey  string
	clientCert string
	clientKey  string
}

func setupCerts(t *testing.T) testEnv {
	t.Helper()
	ca := newCA(t)
	dir := t.TempDir()
	sc, sk := ca.issue(t, "nyttigd", "localhost")
	cc, ck := ca.issue(t, "nyttig-client", "")
	return testEnv{
		caFile:     writeFile(t, dir, "ca.pem", ca.certPEM),
		serverCert: writeFile(t, dir, "server.pem", sc),
		serverKey:  writeFile(t, dir, "server.key", sk),
		clientCert: writeFile(t, dir, "client.pem", cc),
		clientKey:  writeFile(t, dir, "client.key", ck),
	}
}

// startServer brings up a gRPC health server with the given creds and returns
// its address plus a stop func.
func startServer(t *testing.T, env testEnv) (addr string, stop func()) {
	t.Helper()
	creds, err := ServerCredentials(ServerOptions{
		CertFile:     env.serverCert,
		KeyFile:      env.serverKey,
		ClientCAFile: env.caFile,
	})
	if err != nil {
		t.Fatalf("ServerCredentials: %v", err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer(grpc.Creds(creds))
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go func() { _ = srv.Serve(lis) }()
	return lis.Addr().String(), srv.Stop
}

func TestMutualTLS_Handshake(t *testing.T) {
	env := setupCerts(t)
	addr, stop := startServer(t, env)
	defer stop()

	creds, err := ClientCredentials(ClientOptions{
		CertFile:   env.clientCert,
		KeyFile:    env.clientKey,
		CAFile:     env.caFile,
		ServerName: "127.0.0.1", // matches the server cert's IP SAN
	})
	if err != nil {
		t.Fatalf("ClientCredentials: %v", err)
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("health check over mTLS failed: %v", err)
	}
}

func TestMutualTLS_RejectsPlaintextClient(t *testing.T) {
	env := setupCerts(t)
	addr, stop := startServer(t, env)
	defer stop()

	// A client with no certificate (insecure) must be refused by the server.
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err == nil {
		t.Fatal("expected plaintext client to be rejected, but the call succeeded")
	}
}

func TestServerCredentials_RequiresAllFields(t *testing.T) {
	env := setupCerts(t)
	cases := []struct {
		name string
		opt  ServerOptions
	}{
		{"missing key", ServerOptions{CertFile: env.serverCert, ClientCAFile: env.caFile}},
		{"missing cert", ServerOptions{KeyFile: env.serverKey, ClientCAFile: env.caFile}},
		{"missing client CA", ServerOptions{CertFile: env.serverCert, KeyFile: env.serverKey}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ServerCredentials(tc.opt); err == nil {
				t.Fatal("expected error for incomplete server options")
			}
		})
	}
}

func TestClientCredentials_RequiresAllFields(t *testing.T) {
	env := setupCerts(t)
	cases := []struct {
		name string
		opt  ClientOptions
	}{
		{"missing key", ClientOptions{CertFile: env.clientCert, CAFile: env.caFile}},
		{"missing cert", ClientOptions{KeyFile: env.clientKey, CAFile: env.caFile}},
		{"missing CA", ClientOptions{CertFile: env.clientCert, KeyFile: env.clientKey}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ClientCredentials(tc.opt); err == nil {
				t.Fatal("expected error for incomplete client options")
			}
		})
	}
}
