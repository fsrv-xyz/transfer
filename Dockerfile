# syntax=docker/dockerfile:1.23.0@sha256:2780b5c3bab67f1f76c781860de469442999ed1a0d7992a5efdf2cffc0e3d769
FROM golang:1.26.1@sha256:cd78d88e00afadbedd272f977d375a6247455f3a4b1178f8ae8bbcb201743a8a AS builder
ARG CI_JOB_ID
ARG CI_COMMIT_SHORT_SHA
WORKDIR /build
COPY . /build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w \
    -X github.com/fsrv-xyz/version.BuildDate=$(date +%F_%H-%M)  \
    -X github.com/fsrv-xyz/version.Version=${CI_JOB_ID} \
    -X github.com/fsrv-xyz/version.Revision=${CI_COMMIT_SHORT_SHA} \
    " -trimpath

FROM debian:sid@sha256:bcd97037d04fa619b2aad55c3bf32b0f4f590dc6c5a77d86b1f9c42b306e2cfc as certs
RUN apt update && apt install -y ca-certificates

FROM scratch
EXPOSE 8080
ENV S3_ENDPOINT="minio:9000"
ENV S3_BUCKET="transfer"
ENV AWS_ACCESS_KEY_ID="minio"
ENV AWS_SECRET_ACCESS_KEY="minio123"
COPY --from=builder /build/transfer /app/transfer
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
CMD ["/app/transfer", "--web.listen-address", ":8080", "--metrics.listen-address", ":8081"]
