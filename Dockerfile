# syntax=docker/dockerfile:1
#FROM golang AS builder
#WORKDIR /build
#COPY . /build
#RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build
FROM alpine:3.6 as alpine
RUN apk add -U --no-cache ca-certificates

FROM scratch
EXPOSE 8080
ENV S3_ENDPOINT="minio:9000"
ENV S3_BUCKET="transfer"
ENV AWS_ACCESS_KEY_ID="minio"
ENV AWS_SECRET_ACCESS_KEY="minio123"
#COPY --from=builder /build/transfer /app/transfer
COPY --from=alpine /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
ADD ./app /app/transfer
CMD ["/app/transfer", "-web.listen-address", ":8080", "-metrics.listen-address", ":8081"]
