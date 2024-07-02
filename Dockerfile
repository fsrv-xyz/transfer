# syntax=docker/dockerfile:1.8.1@sha256:e87caa74dcb7d46cd820352bfea12591f3dba3ddc4285e19c7dcd13359f7cefd
FROM golang:1.22.4@sha256:3589439790974ec05491b66b656bf1048d0f50dd010a903463e3156ba1fc26de AS builder
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
CMD ["/app/transfer", "--web.listen-address", ":8080", "--metrics.listen-address", ":8081"]
