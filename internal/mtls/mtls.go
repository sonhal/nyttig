// Package mtls builds mutual-TLS gRPC transport credentials for the Nyttig
// daemon and client.
//
// Mutual TLS means both peers present X.509 certificates: the daemon proves its
// identity to the client (so the client knows it is talking to the real server)
// and the client proves its identity to the daemon (so only holders of a
// client certificate signed by the trusted CA can connect). This lets nyttigd
// be exposed on a network — e.g. over a TCP port on a server — without an extra
// authentication layer.
//
// Certificates are PEM files on disk. A typical deployment uses a single
// private CA that signs one server certificate and one (or more) client
// certificates; the server is given the CA bundle to verify clients, and each
// client is given the same CA bundle to verify the server.
package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc/credentials"
)

// minTLSVersion is the floor for all Nyttig TLS connections. TLS 1.3 drops the
// legacy cipher-suite negotiation and is supported everywhere this runs.
const minTLSVersion = tls.VersionTLS13

// ServerOptions configures mutual-TLS credentials for the gRPC server.
type ServerOptions struct {
	CertFile     string // Server certificate chain (PEM).
	KeyFile      string // Server private key (PEM).
	ClientCAFile string // CA bundle used to verify client certificates (PEM).
}

// Enabled reports whether any TLS option was provided. It is used to decide
// whether to configure TLS at all; a partially-filled set is rejected by
// ServerCredentials so misconfiguration fails loudly rather than silently
// falling back to plaintext.
func (o ServerOptions) Enabled() bool {
	return o.CertFile != "" || o.KeyFile != "" || o.ClientCAFile != ""
}

// ServerCredentials builds gRPC transport credentials that enforce mutual TLS:
// the server presents CertFile/KeyFile, and every client must present a
// certificate signed by a CA in ClientCAFile.
func ServerCredentials(o ServerOptions) (credentials.TransportCredentials, error) {
	if o.CertFile == "" || o.KeyFile == "" {
		return nil, fmt.Errorf("mtls: both tls cert and key are required")
	}
	if o.ClientCAFile == "" {
		return nil, fmt.Errorf("mtls: client CA is required to verify clients (mutual TLS)")
	}

	cert, err := tls.LoadX509KeyPair(o.CertFile, o.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("mtls: load server keypair: %w", err)
	}
	clientCAs, err := loadCAPool(o.ClientCAFile)
	if err != nil {
		return nil, err
	}

	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    clientCAs,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   minTLSVersion,
	}), nil
}

// ClientOptions configures mutual-TLS credentials for the gRPC client.
type ClientOptions struct {
	CertFile   string // Client certificate chain (PEM).
	KeyFile    string // Client private key (PEM).
	CAFile     string // CA bundle used to verify the server certificate (PEM).
	ServerName string // Overrides the name verified against the server cert (optional).
}

// Enabled reports whether any TLS option was provided. See ServerOptions.Enabled.
func (o ClientOptions) Enabled() bool {
	return o.CertFile != "" || o.KeyFile != "" || o.CAFile != "" || o.ServerName != ""
}

// ClientCredentials builds gRPC transport credentials for connecting to an
// mTLS-protected daemon: the client presents CertFile/KeyFile and verifies the
// server's certificate against CAFile.
func ClientCredentials(o ClientOptions) (credentials.TransportCredentials, error) {
	if o.CertFile == "" || o.KeyFile == "" {
		return nil, fmt.Errorf("mtls: both client tls cert and key are required")
	}
	if o.CAFile == "" {
		return nil, fmt.Errorf("mtls: CA is required to verify the daemon certificate")
	}

	cert, err := tls.LoadX509KeyPair(o.CertFile, o.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("mtls: load client keypair: %w", err)
	}
	rootCAs, err := loadCAPool(o.CAFile)
	if err != nil {
		return nil, err
	}

	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      rootCAs,
		ServerName:   o.ServerName,
		MinVersion:   minTLSVersion,
	}), nil
}

// loadCAPool reads a PEM CA bundle into a cert pool.
func loadCAPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mtls: read CA %s: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("mtls: no PEM certificates found in %s", path)
	}
	return pool, nil
}
