#!/bin/bash -x

set -o errexit
set -o nounset
set -o pipefail

source "$(dirname "$0")/functions.sh"

setup
trap "teardown" EXIT

# Upload file
curl -s -o "${response_file_path}" --upload-file "${testfile_path}" -w "%{http_code}" "${URL}" | grep -q 200
response_url="$(cat "${response_file_path}")"

# Check response link file name
grep -q "$(basename "${testfile_path}")" "${response_file_path}"

# Download file
curl -s -o "${response_file_path}" "$(cat "${response_file_path}")" -w "%{http_code}" | grep -q 200

# Compare files
cmp -s "${testfile_path}" "${response_file_path}"

# Download checksum
curl -s -o "${response_file_path}" "${response_url}/sum" -w "%{http_code}" | grep -q 200

# Check checksum
awk -v path="${testfile_path}" '{print $1 " " path}' "${response_file_path}" | sha512sum --check
