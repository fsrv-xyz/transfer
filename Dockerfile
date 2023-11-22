# syntax=docker/dockerfile:1
FROM golang:1.21@sha256:daa9d1038a8603b5e12fa3f0ca6e0f4795960eff4a6123fb51d84b2b8469ebc4 AS builder
ARG CI_JOB_ID
ARG CI_COMMIT_SHORT_SHA
WORKDIR /build
COPY . /build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w \
    -X github.com/fsrv-xyz/version.BuildDate=$(date +%F_%H-%M)  \
    -X github.com/fsrv-xyz/version.Version=${CI_JOB_ID} \
    -X github.com/fsrv-xyz/version.Revision=${CI_COMMIT_SHORT_SHA} \
    " -trimpath

FROM debian:sid-20230109@sha256:43b3f2acda18dd4aef3b094f6f79b920c8704a30475b5f11c3f7f0e9c599d699 as certs
RUN apt update && apt install -y ca-certificates

FROM scratch
EXPOSE 8080
ENV S3_ENDPOINT="minio:9000"
ENV S3_BUCKET="transfer"
ENV AWS_ACCESS_KEY_ID="minio"
ENV AWS_SECRET_ACCESS_KEY="minio123"
COPY --from=builder /build/transfer /app/transfer
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
CMD ["/app/transfer", "-web.listen-address", ":8080", "-metrics.listen-address", ":8081"]
