package main

import (
	"flag"
	"fmt"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
)

const (
	_ int64 = iota
	_       = 1 << (10 * iota) // kilobyte
	_                          // megabyte
	GB
)

type Config struct {
	logger      *log.Logger
	minioClient *minio.Client
}

type Parameters struct {
	S3AccessKey   string
	S3SecretKey   string
	S3Endpoint    string
	S3BucketName  string
	UploadLimitGB int64
	ListenAddress string
}

var p Parameters

func init() {
	flag.StringVar(&p.S3Endpoint, "s3.endpoint", "", "address to s3 endpoint")
	flag.StringVar(&p.S3AccessKey, "s3.access", "", "s3 access key")
	flag.StringVar(&p.S3SecretKey, "s3.secret", "", "s3 secret key")
	flag.StringVar(&p.S3BucketName, "s3.bucket", "", "s3 storage bucket")
	flag.StringVar(&p.ListenAddress, "web.listen-address", "", "web server listen address")
	flag.Int64Var(&p.UploadLimitGB, "upload.limit", 1, "Upload limit in GiB")
	flag.Parse()

	if os.Getenv("S3_ENDPOINT") != "" {
		p.S3Endpoint = os.Getenv("S3_ENDPOINT")
	}
	if os.Getenv("AWS_ACCESS_KEY_ID") != "" {
		p.S3AccessKey = os.Getenv("AWS_ACCESS_KEY_ID")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
		p.S3SecretKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
	}
	if os.Getenv("S3_BUCKET") != "" {
		p.S3BucketName = os.Getenv("S3_BUCKET")
	}

	if p.S3AccessKey == "" || p.S3SecretKey == "" || p.S3Endpoint == "" || p.S3BucketName == "" {
		fmt.Println("no s3 details given")
		os.Exit(1)
	}
}

func main() {
	var c = Config{}
	var err error

	c.logger = log.New(os.Stdout, "", log.Lshortfile|log.Lmsgprefix)
	c.minioClient, err = minio.New(p.S3Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(p.S3AccessKey, p.S3SecretKey, ""),
		Secure: false,
	})
	if err != nil {
		c.logger.Println(err)
		os.Exit(1)
	}

	r := mux.NewRouter()
	r.HandleFunc("/{id}/{filename}", c.DownloadHandler).Methods(http.MethodGet)
	r.HandleFunc("/{filename}", c.UploadHandler).Methods(http.MethodPut)

	c.logger.Printf("Listening on %+q\n", p.ListenAddress)
	if err := http.ListenAndServe(p.ListenAddress, r); err != nil {
		log.Fatalln(err)
	}
}

func (c *Config) DownloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, idOK := vars["id"]
	filename, filenameOK := vars["filename"]
	if !idOK || !filenameOK {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	filePath := fmt.Sprintf("%s/%s", id, filename)
	info, err := c.minioClient.StatObject(r.Context(), p.S3BucketName, filePath, minio.StatObjectOptions{})
	if err != nil {
		w.WriteHeader(minio.ToErrorResponse(err).StatusCode)
		c.logger.Printf("%+v", err)
		return
	}

	w.Header().Set("Content-Type", info.ContentType)
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)

	reader, err := c.minioClient.GetObject(r.Context(), p.S3BucketName, info.Key, minio.GetObjectOptions{})
	if err != nil {
		c.logger.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

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

	id := uuid.New()
	_, err := c.minioClient.PutObject(r.Context(), p.S3BucketName, id.String()+"/"+filename, r.Body, r.ContentLength, minio.PutObjectOptions{ContentType: r.Header.Get("Content-Type")})
	if err != nil {
		c.logger.Println(err)
		w.WriteHeader(minio.ToErrorResponse(err).StatusCode)
		return
	}

	// generate download link
	fmt.Fprintf(w, "%s/%s/%s", r.Host, id.String(), filename)
}
