# syntax=docker/dockerfile:1.8.0@sha256:d6d396f3780b1dd56a3acbc975f57bd2fc501989b50164c41387c42d04e780d0
FROM golang:1.22.4@sha256:245c739b7dc876b87e90eabfdbab6bfb14e7e909b7bf260013594937521a311c AS builder
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
