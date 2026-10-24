#!/bin/sh
set -eu

certificate_dir=${1:-certs}
mkdir -p "$certificate_dir"
chmod 700 "$certificate_dir"

files="node-ca.crt node-ca.key control-ca.crt control-ca.key node.crt node.key control-worker.crt control-worker.key"
existing=0
missing=0
for file in $files; do
	if [ -f "$certificate_dir/$file" ]; then
		existing=$((existing + 1))
	else
		missing=$((missing + 1))
	fi
done
if [ "$existing" -eq 8 ]; then
	echo "Local Node mTLS certificates already exist in $certificate_dir"
	exit 0
fi
if [ "$existing" -ne 0 ]; then
	echo "Refusing to replace an incomplete certificate set in $certificate_dir" >&2
	exit 1
fi

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

make_ca() {
	name=$1
	openssl req -x509 -newkey rsa:3072 -nodes -sha256 -days 3650 \
		-subj "/CN=Isodo local $name CA" \
		-keyout "$certificate_dir/$name-ca.key" -out "$certificate_dir/$name-ca.crt" >/dev/null 2>&1
	chmod 600 "$certificate_dir/$name-ca.key"
}

make_leaf() {
	name=$1
	common_name=$2
	ca=$3
	usage=$4
	san=$5
	openssl req -new -newkey rsa:3072 -nodes -sha256 \
		-subj "/CN=$common_name" \
		-keyout "$certificate_dir/$name.key" -out "$tmp_dir/$name.csr" >/dev/null 2>&1
	cat >"$tmp_dir/$name.ext" <<EOF
basicConstraints=CA:FALSE
keyUsage=digitalSignature,keyEncipherment
extendedKeyUsage=$usage
subjectAltName=$san
EOF
	openssl x509 -req -sha256 -days 825 \
		-in "$tmp_dir/$name.csr" \
		-CA "$certificate_dir/$ca-ca.crt" -CAkey "$certificate_dir/$ca-ca.key" \
		-CAcreateserial -extfile "$tmp_dir/$name.ext" \
		-out "$certificate_dir/$name.crt" >/dev/null 2>&1
	chmod 600 "$certificate_dir/$name.key"
}

make_ca node
make_ca control
make_leaf node isodo-node.local node serverAuth 'DNS:isodo-node.local'
make_leaf control-worker isodo-control-worker control clientAuth 'URI:spiffe://isodo/control-worker'
rm -f "$certificate_dir"/*.srl
echo "Created local-only Node mTLS certificates in $certificate_dir"
