#!/usr/bin/env bash
set -xeuo pipefail

export DEBIAN_FRONTEND=noninteractive

URL="https://${DEFAULT_INGRESS_URL}"
apt update -qq && apt install -y -qq curl >/dev/null
dd if=/dev/urandom of=./testfile.bin bs=1M count=1000

checksum="$(sha512sum ./testfile.bin | awk '{print $1}')"

curl --upload-file ./testfile.bin "$URL" | tee ./testfile.url
curl "$(cat ./testfile.url)" -o ./testfile.downloaded

transfer_checksum="$(curl "$(cat ./testfile.url)/sum" | awk '{print $1}')"
if [ "$checksum" != "$transfer_checksum" ]; then
    echo "Checksums do not match!"
    exit 1
fi

downloaded_checksum="$(sha512sum ./testfile.downloaded | awk '{print $1}')"
if [ "$checksum" != "$downloaded_checksum" ]; then
    echo "Checksums do not match!"
    exit 1
fi

