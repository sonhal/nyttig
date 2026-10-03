#!/usr/bin/env bash
#
# gen-certs.sh — generate a private CA plus server and client certificates for
# running nyttigd with mutual TLS.
#
# Usage:
#   scripts/gen-certs.sh [OUT_DIR] [SERVER_HOST...]
#
#   OUT_DIR      Directory to write certs into (default: ./certs)
#   SERVER_HOST  DNS names or IP addresses (IPv4 or IPv6, brackets optional)
#                the client will connect to and that the server certificate
#                is valid for (default: localhost). Give several to dial the
#                daemon by name and by address:
#                  scripts/gen-certs.sh ./certs nyttig.example.com 2001:db8::10
#
# Produces in OUT_DIR:
#   ca.pem / ca.key          the certificate authority
#   server.pem / server.key  the daemon (nyttigd) certificate, signed by the CA
#   client.pem / client.key  a client (nyttig) certificate, signed by the CA
#
# Wire them up:
#   nyttigd -socket :9090 \
#     -tls-cert certs/server.pem -tls-key certs/server.key -tls-client-ca certs/ca.pem
#   nyttig -socket SERVER_HOST:9090 \
#     -tls-cert certs/client.pem -tls-key certs/client.key -tls-ca certs/ca.pem
#
# Both the server and client trust the same CA (ca.pem). Keep the *.key files
# private; distribute client.pem/client.key (and ca.pem) to each client.
set -euo pipefail

OUT_DIR="${1:-./certs}"
if [[ $# -gt 0 ]]; then shift; fi
SERVER_HOSTS=("$@")
[[ ${#SERVER_HOSTS[@]} -gt 0 ]] || SERVER_HOSTS=(localhost)
DAYS=825 # ~27 months; under the 825-day cap some TLS stacks enforce.

mkdir -p "$OUT_DIR"
cd "$OUT_DIR"

# Build a SAN extension: an IPv4 address or anything with a colon (IPv6) is
# an IP entry, everything else a DNS name. A client dialing an address only
# accepts an IP entry, so "DNS:2001:db8::10" would never match. Loopback is
# always included for local testing.
SERVER_SAN=""
add_san() {
  [[ ",${SERVER_SAN}," == *",$1,"* ]] || SERVER_SAN="${SERVER_SAN:+${SERVER_SAN},}$1"
}
for host in "${SERVER_HOSTS[@]}"; do
  host="${host#[}"; host="${host%]}"
  if [[ "$host" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ || "$host" == *:* ]]; then
    add_san "IP:${host}"
  else
    add_san "DNS:${host}"
  fi
done
add_san "IP:127.0.0.1"
add_san "IP:::1"
add_san "DNS:localhost"
SERVER_HOST="${SERVER_HOSTS[0]#[}"; SERVER_HOST="${SERVER_HOST%]}"

echo ">> Generating CA"
openssl ecparam -name prime256v1 -genkey -noout -out ca.key
openssl req -x509 -new -key ca.key -sha256 -days "$DAYS" \
  -subj "/CN=nyttig-ca" -out ca.pem

gen_leaf() {
  local name="$1" cn="$2" ext="$3"
  echo ">> Generating ${name} certificate (CN=${cn})"
  openssl ecparam -name prime256v1 -genkey -noout -out "${name}.key"
  openssl req -new -key "${name}.key" -subj "/CN=${cn}" -out "${name}.csr"
  openssl x509 -req -in "${name}.csr" -CA ca.pem -CAkey ca.key -CAcreateserial \
    -days "$DAYS" -sha256 -extfile <(printf '%s\n' "$ext") -out "${name}.pem"
  rm -f "${name}.csr"
}

gen_leaf server "$SERVER_HOST" \
  "subjectAltName=${SERVER_SAN}
extendedKeyUsage=serverAuth"

gen_leaf client "nyttig-client" \
  "extendedKeyUsage=clientAuth"

rm -f ca.srl
chmod 600 ./*.key

echo
echo "Done. Certificates written to: $(pwd)"
echo "  CA:     ca.pem"
echo "  Server: server.pem / server.key  (valid for: ${SERVER_SAN})"
echo "  Client: client.pem / client.key"
