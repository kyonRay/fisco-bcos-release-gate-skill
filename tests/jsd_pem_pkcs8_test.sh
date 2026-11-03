#!/usr/bin/env bash
# java-sdk's PEMKeyStore reads PKCS#8 only. build_chain + Tongsuo write the account key as SEC1
# "EC PRIVATE KEY", and the first 3.18.0 jsd round died on "encoded key spec not recognized"
# before its first transaction. _jsd_pkcs8_pem converts on the way into account/ecdsa.
set -euo pipefail
cd "$(dirname "$0")/.."
source tests/assert.sh
source scripts/scenarios/scenario_jsd.sh

command -v openssl >/dev/null || { echo "SKIP: no openssl on PATH"; exit 0; }
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
openssl ecparam -name secp256k1 -genkey -noout -out "$tmp/sec1.pem" 2>/dev/null \
    || { echo "SKIP: this openssl cannot generate a secp256k1 SEC1 key"; exit 0; }
assert_contains "$(head -n1 "$tmp/sec1.pem")" "BEGIN EC PRIVATE KEY" "fixture is a SEC1 key"

_jsd_pkcs8_pem "$tmp/sec1.pem" "$tmp/out.pem"
assert_eq "-----BEGIN PRIVATE KEY-----" "$(head -n1 "$tmp/out.pem")" "SEC1 is converted to PKCS#8"
# Same key: the public point must survive the conversion.
assert_eq "$(openssl ec -in "$tmp/sec1.pem" -pubout 2>/dev/null)" "$(openssl ec -in "$tmp/out.pem" -pubout 2>/dev/null)" "conversion keeps the key"

_jsd_pkcs8_pem "$tmp/out.pem" "$tmp/again.pem"
assert_eq "$(cat "$tmp/out.pem")" "$(cat "$tmp/again.pem")" "a PKCS#8 key is copied unchanged"
assert_done
