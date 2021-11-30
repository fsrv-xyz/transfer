package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	_ = 1 << (iota * 10)
	KB
	MB
	GB
)

var (
	metricObjectAction = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "transfer",
		Name:      "object_action",
		Help:      "Actions applied to objects",
	}, []string{"action"})

	metricObjectSize = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "transfer",
		Name:      "object_size_bytes",
		Help:      "Uploaded objects by size",
		Buckets:   []float64{1 * KB, 10 * KB, 100 * KB, 1 * MB, 10 * MB, 100 * MB, 300 * MB, 600 * MB, 900 * MB},
	})
)

type Config struct {
	logger      *log.Logger
	minioClient *minio.Client
}

type Parameters struct {
	CleanupInterval      int
	ListenAddress        string
	MetricsListenAddress string
	DownloadLinkPrefix   string
	S3Endpoint           string
	S3AccessKey          string
	S3SecretKey          string
	S3BucketName         string
	S3UseSecurity        bool
	UploadLimitGB        int64
}

var p Parameters

func init() {
	flag.StringVar(&p.ListenAddress, "web.listen-address", ":8080", "web server listen address")
	flag.StringVar(&p.MetricsListenAddress, "metrics.listen-address", "127.0.0.1:9042", "metrics endpoint listen address")
	flag.Int64Var(&p.UploadLimitGB, "upload.limit", 1, "Upload limit in GiB")
	flag.IntVar(&p.CleanupInterval, "cleanup.interval", 60, "interval in seconds for cleanup")
	flag.StringVar(&p.S3Endpoint, "s3.endpoint", "", "address to s3 endpoint")
	flag.StringVar(&p.S3AccessKey, "s3.access", "", "s3 access key")
	flag.StringVar(&p.S3SecretKey, "s3.secret", "", "s3 secret key")
	flag.StringVar(&p.S3BucketName, "s3.bucket", "", "s3 storage bucket")
	flag.BoolVar(&p.S3UseSecurity, "s3.secure", true, "use tls for connection")
	flag.StringVar(&p.DownloadLinkPrefix, "link.prefix", "http", "prepending stuff for download link")
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

func metricsWebListener() {
	http.Handle("/metrics", promhttp.Handler())
	if http.ListenAndServe(p.MetricsListenAddress, nil) != nil {
		log.Fatalln("webserver start failed")
	}
}

func main() {
	var c = Config{}
	var err error

	c.logger = log.New(os.Stdout, "", log.Lshortfile|log.Lmsgprefix)
	c.minioClient, err = minio.New(p.S3Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(p.S3AccessKey, p.S3SecretKey, ""),
		Secure: p.S3UseSecurity,
	})
	if err != nil {
		c.logger.Println(err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool)

	// catch interrupts
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		for sig := range sigChan {
			fmt.Println(sig.String())
			cancel()

			<-done
			os.Exit(1)
		}
	}()

	r := mux.NewRouter()
	r.HandleFunc("/{id}/{filename}", c.DownloadHandler).Methods(http.MethodGet)
	r.HandleFunc("/{filename}", c.UploadHandler).Methods(http.MethodPut)

	// start cleanup worker and metrics server
	go c.CleanupWorker(ctx, done)
	go metricsWebListener()

	// start webserver
	c.logger.Printf("Listening on %+q\n", p.ListenAddress)
	if err := http.ListenAndServe(p.ListenAddress, r); err != nil {
		log.Fatalln(err)
	}
}

func (c *Config) CleanupWorker(ctx context.Context, done chan<- bool) {
	var sleepCounter int
	for {
		select {
		case <-ctx.Done():
			done <- true
			return
		default:
			if sleepCounter%p.CleanupInterval != 0 {
				break
			}
			for object := range c.minioClient.ListObjects(ctx, p.S3BucketName, minio.ListObjectsOptions{Recursive: true}) {
				if object.LastModified.Add(1 * time.Hour).Before(time.Now()) {
					c.logger.Printf("[cleanup] - remove %+v\n", object.Key)
					metricObjectAction.With(prometheus.Labels{"action": "delete"}).Inc()
					if err := c.minioClient.RemoveObject(ctx, p.S3BucketName, object.Key, minio.RemoveObjectOptions{}); err != nil {
						c.logger.Println(err)
					}
				}
			}
			sleepCounter = 0
		}
		time.Sleep(1 * time.Second)
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
	object, err := c.minioClient.StatObject(r.Context(), p.S3BucketName, filePath, minio.StatObjectOptions{})
	if err != nil {
		w.WriteHeader(minio.ToErrorResponse(err).StatusCode)
		c.logger.Printf("%+v", err)
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

	id := uuid.New()
	object, err := c.minioClient.PutObject(r.Context(), p.S3BucketName, id.String()+"/"+filename, r.Body, r.ContentLength, minio.PutObjectOptions{
		ContentType: r.Header.Get("Content-Type"),
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
