package main

import (
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"github.com/getsentry/sentry-go"
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
	handlerMainSpan := sentry.StartSpan(r.Context(), "handler.download")
	defer handlerMainSpan.Finish() 

	handlerMainSpan.Data = make(map[string]interface{})
	handlerMainSpan.Data["host"] = "this is a test"

	vars := mux.Vars(r)
	// check if handler is called with /.../.../sum
	_, sumMode := vars["sum"]

	id, idOK := vars["id"]
	filename, filenameOK := vars["filename"]
	if !idOK || !filenameOK {
		sentry.CaptureException(fmt.Errorf("id or filename not provided"))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if cancelRequestIfUnhealthy(w) {
		return
	}

	statSpan := handlerMainSpan.StartChild("object.stat")

	filePath := fmt.Sprintf("%s/%s", id, filename)
	object, err := c.minioClient.StatObject(r.Context(), p.S3BucketName, filePath, minio.StatObjectOptions{})
	if err != nil {
		switch minio.ToErrorResponse(err).StatusCode {
		case http.StatusNotFound:
			statSpan.Status = sentry.SpanStatusNotFound
		default:
			statSpan.Status = sentry.SpanStatusInternalError
		}
		sentry.CaptureMessage(fmt.Sprintf("%s: %s", err.Error(), r.URL.String()))
		statSpan.Finish()
		w.WriteHeader(minio.ToErrorResponse(err).StatusCode)
		traceLog(c.logger, err)
		return
	}
	statSpan.Finish()

	// only return checksum when called in sum mode
	if sumMode {
		metricObjectAction.With(prometheus.Labels{"action": "sum"}).Inc()
		_, httpResponseError := fmt.Fprintf(w, "%s  %s\n", object.UserMetadata[ChecksumMetadataFieldName], filename)
		if httpResponseError != nil {
			sentry.CaptureMessage(fmt.Sprintf("%s: %s", err.Error(), r.URL.String()))
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	w.Header().Set("Content-Type", object.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(object.Size, 10))
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)

	objectGetSpan := handlerMainSpan.StartChild("object.get")
	reader, err := c.minioClient.GetObject(r.Context(), p.S3BucketName, object.Key, minio.GetObjectOptions{})
	if err != nil {
		objectGetSpan.Status = sentry.SpanStatusInternalError
		objectGetSpan.Finish()
		traceLog(c.logger, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	objectGetSpan.Finish()

	metricObjectAction.With(prometheus.Labels{"action": "download"}).Inc()

	objectCopySpan := handlerMainSpan.StartChild("object.copy")
	defer objectCopySpan.Finish()
	if _, copyError := io.Copy(w, reader); err != nil {
		objectCopySpan.Status = sentry.SpanStatusInternalError
		objectCopySpan.Finish()
		traceLog(c.logger, copyError)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func (c *Config) UploadHandler(w http.ResponseWriter, r *http.Request) {
	handlerMainSpan := sentry.StartSpan(r.Context(), "handler.upload")
	defer handlerMainSpan.Finish()

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
		sentry.CaptureMessage("upload too large")
		w.WriteHeader(http.StatusNotAcceptable)
		return
	}

	metadata := make(map[string]string)
	sha512SumGenerator := sha512.New()

	buf := &bytes.Buffer{}
	tee := io.TeeReader(r.Body, buf)

	copySpan := handlerMainSpan.StartChild("object.copy")

	written, err := io.CopyN(sha512SumGenerator, tee, r.ContentLength)
	if written != r.ContentLength {
		traceLog(c.logger, err)
		sentry.CaptureException(err)
		copySpan.Status = sentry.SpanStatusInternalError
		copySpan.Finish()
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	metadata[ChecksumMetadataFieldName] = hex.EncodeToString(sha512SumGenerator.Sum(nil))
	copySpan.Finish()

	objectForwardSpan := handlerMainSpan.StartChild("object.put")
	id := uuid.New()
	_, err = c.minioClient.PutObject(r.Context(), p.S3BucketName, id.String()+"/"+filename, buf, r.ContentLength, minio.PutObjectOptions{
		ContentType:  selectContentType(filename),
		UserMetadata: metadata,
	})
	objectForwardSpan.Finish()

	if err != nil {
		traceLog(c.logger, err)
		sentry.CaptureException(err)
		objectForwardSpan.Status = sentry.SpanStatusInternalError
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
