package main

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/bonsai-oss/mux"
	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
	"github.com/prometheus/client_golang/prometheus"

	"transfer/internal/metrics"
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

	transaction := sentry.TransactionFromContext(r.Context())
	transaction.Status = sentry.SpanStatusOK

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
	statSpan.Status = sentry.SpanStatusOK

	filePath := fmt.Sprintf("%s/%s", id, filename)
	object, err := c.minioClient.StatObject(statSpan.Context(), p.S3BucketName, filePath, minio.StatObjectOptions{})
	if err != nil {
		switch minio.ToErrorResponse(err).StatusCode {
		case http.StatusNotFound:
			statSpan.Status = sentry.SpanStatusNotFound
			transaction.Status = sentry.SpanStatusNotFound
		default:
			statSpan.Status = sentry.SpanStatusInternalError
			transaction.Status = sentry.SpanStatusInternalError
		}
		sentry.CaptureException(fmt.Errorf("%s: %s", err.Error(), r.URL.String()))
		statSpan.Finish()
		w.WriteHeader(minio.ToErrorResponse(err).StatusCode)
		traceLog(c.logger, err)
		return
	}
	statSpan.Data = map[string]interface{}{
		"object": object,
	}
	statSpan.Finish()

	// only return checksum when called in sum mode
	if sumMode {
		metrics.ObjectAction.With(prometheus.Labels{metrics.LabelAction: "sum"}).Inc()
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
	reader, err := c.minioClient.GetObject(objectGetSpan.Context(), p.S3BucketName, object.Key, minio.GetObjectOptions{})
	if err != nil {
		objectGetSpan.Status = sentry.SpanStatusInternalError
		objectGetSpan.Finish()
		traceLog(c.logger, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	objectGetSpan.Finish()

	metrics.ObjectAction.With(prometheus.Labels{metrics.LabelAction: "download"}).Inc()

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
	filename = onlyAllowedCharacters(filename)

	if cancelRequestIfUnhealthy(w) {
		return
	}

	if !ok || filename == "" {
		http.Error(w, "filename not provided", http.StatusBadRequest)
		return
	}
	if r.ContentLength > p.UploadLimitGB*metrics.GB {
		sentry.CaptureMessage("upload too large")
		http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
		return
	}

	metadata := make(map[string]string)
	sha512SumGenerator := sha512.New()

	pipeReader, pipeWriter := io.Pipe()
	multiWriter := io.MultiWriter(sha512SumGenerator, pipeWriter)

	go func() {
		copySpan := handlerMainSpan.StartChild("object.copy")
		defer copySpan.Finish()
		_, err := io.CopyN(multiWriter, r.Body, r.ContentLength)
		pipeWriter.CloseWithError(err)
	}()

	objectForwardSpan := handlerMainSpan.StartChild("object.put")

	prefixId := uuid.NewString()

	uploadedObject, uploadError := c.minioClient.PutObject(objectForwardSpan.Context(), p.S3BucketName, prefixId+"/"+filename, pipeReader, r.ContentLength, minio.PutObjectOptions{
		ContentType: selectContentType(filename),
	})

	if uploadError != nil {
		traceLog(c.logger, uploadError)
		sentry.CaptureException(uploadError)
		objectForwardSpan.Status = sentry.SpanStatusInternalError
		w.WriteHeader(minio.ToErrorResponse(uploadError).StatusCode)
		return
	}

	metadata[ChecksumMetadataFieldName] = hex.EncodeToString(sha512SumGenerator.Sum(nil))
	objectMetadataSpan := handlerMainSpan.StartChild("object.put.metadata")
	_, copyError := c.minioClient.CopyObject(objectMetadataSpan.Context(), minio.CopyDestOptions{
		Bucket:          p.S3BucketName,
		Object:          uploadedObject.Key,
		UserMetadata:    metadata,
		ReplaceMetadata: true,
	}, minio.CopySrcOptions{
		Bucket: uploadedObject.Bucket,
		Object: uploadedObject.Key,
	})
	objectMetadataSpan.Finish()
	if copyError != nil {
		traceLog(c.logger, copyError)
		sentry.CaptureException(copyError)
		objectForwardSpan.Status = sentry.SpanStatusInternalError
		w.WriteHeader(minio.ToErrorResponse(copyError).StatusCode)
		return
	}

	objectForwardSpan.Finish()

	metrics.ObjectSize.Observe(float64(r.ContentLength))
	metrics.ObjectAction.With(prometheus.Labels{metrics.LabelAction: "upload"}).Inc()

	downloadLink := fmt.Sprintf("%s://%s/%s/%s\n", p.DownloadLinkPrefix, r.Host, prefixId, filename)

	// generate download link
	_, downloadLinkResponseError := fmt.Fprint(w, downloadLink)
	handlerMainSpan.Data = map[string]interface{}{
		"download_link": downloadLink,
	}
	if downloadLinkResponseError != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}
