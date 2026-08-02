#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CERT_DIR="${ROOT_DIR}/certs"
mkdir -p "${CERT_DIR}"

openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "${CERT_DIR}/ca.key" \
  -out "${CERT_DIR}/ca.crt" \
  -days 3650 \
  -subj "/CN=distributed-workflow-test-ca"

openssl req -newkey rsa:2048 -nodes \
  -keyout "${CERT_DIR}/iam.key" \
  -out "${CERT_DIR}/iam.csr" \
  -subj "/CN=iam"

openssl x509 -req -in "${CERT_DIR}/iam.csr" -CA "${CERT_DIR}/ca.crt" -CAkey "${CERT_DIR}/ca.key" \
  -CAcreateserial -out "${CERT_DIR}/iam.crt" -days 3650 \
  -extfile <(printf "subjectAltName=DNS:iam,DNS:localhost,DNS:host.docker.internal,IP:127.0.0.1")

openssl req -newkey rsa:2048 -nodes \
  -keyout "${CERT_DIR}/echo.key" \
  -out "${CERT_DIR}/echo.csr" \
  -subj "/CN=protected-echo"

openssl x509 -req -in "${CERT_DIR}/echo.csr" -CA "${CERT_DIR}/ca.crt" -CAkey "${CERT_DIR}/ca.key" \
  -CAcreateserial -out "${CERT_DIR}/echo.crt" -days 3650 \
  -extfile <(printf "subjectAltName=DNS:protected-echo,DNS:localhost,DNS:host.docker.internal,IP:127.0.0.1")

openssl req -newkey rsa:2048 -nodes \
  -keyout "${CERT_DIR}/envoy.key" \
  -out "${CERT_DIR}/envoy.csr" \
  -subj "/CN=envoy"

openssl x509 -req -in "${CERT_DIR}/envoy.csr" -CA "${CERT_DIR}/ca.crt" -CAkey "${CERT_DIR}/ca.key" \
  -CAcreateserial -out "${CERT_DIR}/envoy.crt" -days 3650 \
  -extfile <(printf "subjectAltName=DNS:envoy,DNS:localhost,DNS:host.docker.internal,IP:127.0.0.1")

rm -f "${CERT_DIR}/"*.csr "${CERT_DIR}/ca.srl"
# Readable by the non-root Envoy process when these files are bind-mounted.
chmod 644 "${CERT_DIR}/"*.crt "${CERT_DIR}/"*.key
echo "wrote certs to ${CERT_DIR}"
