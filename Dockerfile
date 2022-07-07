# syntax=docker/dockerfile:1
FROM golang AS builder
WORKDIR /build
COPY . /build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "\
                -s -w \
                -X golang.fsrv.services/version.Version=${DISTVERSIONPREFIX}${DISTVERSION} \
                -X golang.fsrv.services/version.Revision="39cee13" \
                -X golang.fsrv.services/version.Branch="master" \
                -X golang.fsrv.services/version.BuildUser=${USER} \
                -X golang.fsrv.services/version.BuildDate=${BUILD_DATE}"

FROM scratch
EXPOSE 8080
ENV S3_ENDPOINT="minio:9000"
ENV S3_BUCKET="transfer"
ENV AWS_ACCESS_KEY_ID="minio"
ENV AWS_SECRET_ACCESS_KEY="minio123"
COPY --from=builder /build/transfer /app/transfer
CMD ["/app/transfer", "-web.listen-address", ":8080", "-metrics.listen-address", ":8081"]
