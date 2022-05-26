package main

import (
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/minio/minio-go/v7"
	"github.com/prometheus/client_golang/prometheus"
)

func (c *Config) HealthCheckHandler(w http.ResponseWriter, _ *http.Request) {
	if backendState != StateHealthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	if _, writeErr := fmt.Fprintln(w, backendState); writeErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (c *Config) DownloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	// check if handler is called with /.../.../sum
	_, sumMode := vars["sum"]

	id, idOK := vars["id"]
	filename, filenameOK := vars["filename"]
	if !idOK || !filenameOK {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if cancelRequestIfUnhealthy(w) {
		return
	}

	filePath := fmt.Sprintf("%s/%s", id, filename)
	object, err := c.minioClient.StatObject(r.Context(), p.S3BucketName, filePath, minio.StatObjectOptions{})
	if err != nil {
		w.WriteHeader(minio.ToErrorResponse(err).StatusCode)
		traceLog(c.logger, err)
		return
	}

	// only return checksum when called in sum mode
	if sumMode {
		metricObjectAction.With(prometheus.Labels{"action": "sum"}).Inc()
		_, httpResponseError := fmt.Fprintf(w, "%s  %s\n", object.UserMetadata[ChecksumMetadataFieldName], filename)
		if httpResponseError != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", object.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.Size, 10))
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)

	reader, err := c.minioClient.GetObject(r.Context(), p.S3BucketName, object.Key, minio.GetObjectOptions{})
	if err != nil {
		traceLog(c.logger, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	metricObjectAction.With(prometheus.Labels{"action": "download"}).Inc()

	if _, copyError := io.Copy(w, reader); err != nil {
		traceLog(c.logger, copyError)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func (c *Config) UploadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	filename, ok := vars["filename"]
	filename = url.QueryEscape(filename)

	if cancelRequestIfUnhealthy(w) {
		return
	}

	if !ok || filename == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if r.ContentLength > p.UploadLimitGB*GB {
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}

	metadata := make(map[string]string)
	sha512SumGenerator := sha512.New()

	buf := &bytes.Buffer{}
	tee := io.TeeReader(r.Body, buf)

	written, err := io.CopyN(sha512SumGenerator, tee, r.ContentLength)
	if written != r.ContentLength {
		traceLog(c.logger, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	metadata[ChecksumMetadataFieldName] = hex.EncodeToString(sha512SumGenerator.Sum(nil))

	id := uuid.New()
	_, err = c.minioClient.PutObject(r.Context(), p.S3BucketName, id.String()+"/"+filename, buf, r.ContentLength, minio.PutObjectOptions{
		ContentType:  selectContentType(filename),
		UserMetadata: metadata,
	})

	if err != nil {
		traceLog(c.logger, err)
		w.WriteHeader(minio.ToErrorResponse(err).StatusCode)
		return
	}

	metricObjectSize.Observe(float64(r.ContentLength))
	metricObjectAction.With(prometheus.Labels{"action": "upload"}).Inc()

	// generate download link
	_, downloadLinkResponseError := fmt.Fprintf(w, "%s://%s/%s/%s\n", p.DownloadLinkPrefix, r.Host, id.String(), filename)
	if downloadLinkResponseError != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}
