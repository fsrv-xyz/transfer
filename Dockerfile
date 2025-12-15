# syntax=docker/dockerfile:1.20.0@sha256:26147acbda4f14c5add9946e2fd2ed543fc402884fd75146bd342a7f6271dc1d
FROM golang:1.25.5@sha256:36b4f45d2874905b9e8573b783292629bcb346d0a70d8d7150b6df545234818f AS builder
ARG CI_JOB_ID
ARG CI_COMMIT_SHORT_SHA
WORKDIR /build
COPY . /build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w \
    -X github.com/fsrv-xyz/version.BuildDate=$(date +%F_%H-%M)  \
    -X github.com/fsrv-xyz/version.Version=${CI_JOB_ID} \
    -X github.com/fsrv-xyz/version.Revision=${CI_COMMIT_SHORT_SHA} \
    " -trimpath

FROM debian:sid@sha256:606d1402fe507038fcd647c416f2b4b79035bd49842d79b3cc8a046d86720149 as certs
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
