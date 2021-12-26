package main

import (
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/minio/minio-go/v7"
	"github.com/prometheus/client_golang/prometheus"
)

func (c *Config) HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	switch backendState {
	case StateUnhealthy:
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "NOT OK")
	case StateHealthy:
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
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

	filePath := fmt.Sprintf("%s/%s", id, filename)
	object, err := c.minioClient.StatObject(r.Context(), p.S3BucketName, filePath, minio.StatObjectOptions{})
	if err != nil {
		w.WriteHeader(minio.ToErrorResponse(err).StatusCode)
		c.logger.Printf("%+v", err)
		return
	}

	// only return checksum when called in sum mode
	if sumMode {
		c.logger.Printf("[sum] - %+v\n", object.Key)
		metricObjectAction.With(prometheus.Labels{"action": "sum"}).Inc()

		fmt.Fprintf(w, "%s  %s\n", object.UserMetadata[ChecksumMetadataFieldName], filename)
		return
	}

	w.Header().Set("Content-Type", object.ContentType)
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)

	reader, err := c.minioClient.GetObject(r.Context(), p.S3BucketName, object.Key, minio.GetObjectOptions{})
	if err != nil {
		c.logger.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	c.logger.Printf("[download] - %+v\n", object.Key)
	metricObjectAction.With(prometheus.Labels{"action": "download"}).Inc()

	if _, copyError := io.Copy(w, reader); err != nil {
		c.logger.Println(copyError)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func (c *Config) UploadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	filename, ok := vars["filename"]
	filename = url.QueryEscape(filename)

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
		c.logger.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	metadata[ChecksumMetadataFieldName] = hex.EncodeToString(sha512SumGenerator.Sum(nil))

	id := uuid.New()
	object, err := c.minioClient.PutObject(r.Context(), p.S3BucketName, id.String()+"/"+filename, buf, r.ContentLength, minio.PutObjectOptions{
		ContentType:  r.Header.Get("Content-Type"),
		UserMetadata: metadata,
	})

	if err != nil {
		c.logger.Println(err)
		w.WriteHeader(minio.ToErrorResponse(err).StatusCode)
		return
	}

	metricObjectSize.Observe(float64(r.ContentLength))

	c.logger.Printf("[upload] - %+v\n", object.Key)
	metricObjectAction.With(prometheus.Labels{"action": "upload"}).Inc()
	// generate download link
	fmt.Fprintf(w, "%s://%s/%s/%s\n", p.DownloadLinkPrefix, r.Host, id.String(), filename)
}
