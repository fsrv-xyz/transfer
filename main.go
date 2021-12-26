package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/gorilla/mux"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ChecksumMetadataFieldName - UserMetadata key for storing the checksum of the file
const ChecksumMetadataFieldName = "Sha512sum"

const (
	_  = iota
	KB = 1 << (10 * iota)
	MB
	GB
)

type State int

const (
	_ State = iota
	StateHealthy
	StateUnhealthy
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

	// declare initial state as unhealthy
	backendState = StateUnhealthy
)

type Config struct {
	logger      *log.Logger
	minioClient *minio.Client
}

type Parameters struct {
	HealthCheckInterval  int
	HealthCheckReturnGap time.Duration
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
	flag.Int64Var(&p.UploadLimitGB, "upload.limit", 2, "Upload limit in GiB")
	flag.IntVar(&p.CleanupInterval, "cleanup.interval", 60, "interval in seconds for cleanup")
	flag.IntVar(&p.HealthCheckInterval, "healthcheck.interval", 2, "interval in seconds for healthcheck")
	flag.DurationVar(&p.HealthCheckReturnGap, "healthcheck.return.gap", 2*time.Second, "time in seconds for declaring the service as healthy after successful check")
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
	if os.Getenv("S3_SECURE") != "" {
		switch os.Getenv("S3_SECURE") {
		case "true":
			p.S3UseSecurity = true
		case "false":
			p.S3UseSecurity = false
		}
	}

	if p.S3AccessKey == "" || p.S3SecretKey == "" || p.S3Endpoint == "" || p.S3BucketName == "" {
		fmt.Println("no s3 details given")
		os.Exit(1)
	}
}

func webListener(server *http.Server, group *sync.WaitGroup) {
	log.Printf("Listening on %+q\n", server.Addr)
	if err := server.ListenAndServe(); err != nil {
		log.Printf("%+v %+q", err, server.Addr)
		group.Done()
	}
}

func main() {
	serverWaiter := sync.WaitGroup{}
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

	applicationRouter := mux.NewRouter()
	applicationRouter.HandleFunc("/{id}/{filename}", c.DownloadHandler).Methods(http.MethodGet)
	applicationRouter.HandleFunc("/{id}/{filename}/{sum:sum}", c.DownloadHandler).Methods(http.MethodGet)
	applicationRouter.HandleFunc("/{filename}", c.UploadHandler).Methods(http.MethodPut)

	metricsRouter := mux.NewRouter()
	metricsRouter.Handle("/metrics", promhttp.Handler()).Methods(http.MethodGet)
	metricsRouter.HandleFunc("/-/ready", c.HealthCheckHandler).Methods(http.MethodGet)
	metricsRouter.HandleFunc("/-/healthy", c.HealthCheckHandler).Methods(http.MethodGet)

	// declare http applicationServer
	servers := []*http.Server{
		{
			Addr:    p.ListenAddress,
			Handler: applicationRouter,
			//ReadTimeout:  10 * time.Second,
			//WriteTimeout: 10 * time.Second,
			//IdleTimeout:  10 * time.Second,
		},
		{
			Addr:    p.MetricsListenAddress,
			Handler: metricsRouter,
		},
	}
	// create one waitGroup entry for each server
	serverWaiter.Add(len(servers))

	// start webservers
	for _, server := range servers {
		go webListener(server, &serverWaiter)
	}

	// create context and return channel for worker tasks
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan interface{})

	// catch interrupts
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	go func() {
		for sig := range sigChan {
			c.logger.Printf("receive signal %+q\n", sig.String())
			// shutdown http servers
			for _, server := range servers {
				if err := server.Shutdown(context.Background()); err != nil {
					c.logger.Fatalln(err)
				}
			}
			cancel() // stop workers
		}
	}()

	// start worker processes
	workerCount := -1
	for _, worker := range []func(context.Context, chan<- interface{}){
		c.CleanupWorker,
		c.HealthCheckWorker,
	} {
		workerCount++
		go worker(ctx, done)
	}

	// wait until cleanup worker is done
	var workersDone int
	for range done {
		if workersDone == workerCount {
			break
		}
		workersDone++
	}
	// wait until servers are shut down
	serverWaiter.Wait()
	c.logger.Println("transfer server terminated")
}
