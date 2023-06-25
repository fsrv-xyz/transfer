#!/usr/bin/env bats

URL="https://${DEFAULT_INGRESS_URL}/"
TESTFILE="./testfile.bin"
TESTFILE_RESULT="./testfile-result.bin"
TESTFILE_RESULT_SUM="./testfile-result.bin.sum"
DOWNLOAD_URL_FILE="/tmp/download-link"

setup_file() {
    echo "Setting up test file"
    dd if=/dev/urandom of=${TESTFILE} bs=1M count=1
}

teardown_file() {
    echo "Removing test file"
    rm -f ${TESTFILE}
    rm -f ${TESTFILE_RESULT}
    rm -f ${TESTFILE_RESULT_SUM}
    rm -f ${DOWNLOAD_URL_FILE}
}

@test "${URL} is reachable" {
    run curl -s -o /dev/null -w "%{http_code}" ${URL}
    [ "$status" -eq 0 ]
    [ "$output" -eq 404 ]
}

@test "upload works" {
    run curl -s -o "$DOWNLOAD_URL_FILE" -w "%{http_code}" --upload-file "${TESTFILE}" "${URL}"
    [ "$status" -eq 0 ]
    [ "$output" -eq 200 ]
}

@test "download url follows the pattern" {
    grep -q -e "^${URL}.*\/$(basename ${TESTFILE})$" "$DOWNLOAD_URL_FILE"
}

@test "file is downloadable" {
    run curl -s -o "$TESTFILE_RESULT" -w "%{http_code}" "$(cat ${DOWNLOAD_URL_FILE})"
    [ "$status" -eq 0 ]
    [ "$output" -eq 200 ]
}

@test "checksum file is downloadable" {
    run curl -s -o "$TESTFILE_RESULT_SUM" -w "%{http_code}" "$(cat ${DOWNLOAD_URL_FILE})/sum"
    [ "$status" -eq 0 ]
    [ "$output" -eq 200 ]
}

@test "downloaded file is the same as uploaded" {
    run diff "${TESTFILE}" "${TESTFILE_RESULT}"
    [ "$status" -eq 0 ]
}

@test "calculated checksum matches" {
    checksum=$(sha512sum "${TESTFILE_RESULT}" | cut -d' ' -f1)
    [ "$checksum" = "$(cat ${TESTFILE_RESULT_SUM} | cut -d' ' -f1)" ]
}