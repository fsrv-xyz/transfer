#!/usr/bin/env bash

declare -r URL="https://${DEFAULT_INGRESS_URL}"

function generate_test_file() {
  local -r file_name="$1"
  local -r file_size="$2"
  local -r file_path="$(mktemp -d)/${file_name}"

  dd if=/dev/urandom of="${file_path}" bs=1M count="${file_size}" > /dev/null 2>&1

  echo "${file_path}"
}

testfile_path=""
response_file_path=""

function setup() {
  testfile_path="$(generate_test_file "test-file-1M" 1)"
  response_file_path="$(mktemp -d)/response"
}

function teardown() {
  rm -f "${testfile_path}"
  rm -f "${response_file_path}"
}