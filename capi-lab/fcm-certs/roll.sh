#!/usr/bin/env bash
set -eo pipefail

echo "generating example certs"
cfssl genkey -initca ca-csr.json | cfssljson -bare ca
cfssl gencert -ca=ca.pem -ca-key=ca-key.pem -config=ca-config.json -profile=client-server tls.json | cfssljson -bare tls
rm *.csr
mv tls.pem tls.crt
mv tls-key.pem tls.key
