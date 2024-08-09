package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/bonsai-oss/mux"
	"github.com/fsrv-xyz/version"
	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"transfer/internal/metrics"
)

// ChecksumMetadataFieldName - UserMetadata key for storing the checksum of the file
const ChecksumMetadataFieldName = "Sha512sum"

type State string

const (
	StateHealthy   State = "healthy"
	StateUnhealthy State = "unhealthy"
)

var (
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
	DisableCleanupWorker bool
}

var p Parameters

func init() {
	// skip init if running in go test environment
	if strings.Contains(os.Args[0], "/_test/") || strings.HasSuffix(os.Args[0], ".test") {
		return
	}

	app := kingpin.New("transfer", "Daemon transferring files to s3 compatible storage")
	app.Flag("web.listen-address", "web server listen address").Default(":8080").StringVar(&p.ListenAddress)
	app.Flag("metrics.listen-address", "metrics endpoint listen address").Default("127.0.0.1:9042").StringVar(&p.MetricsListenAddress)
	app.Flag("upload.limit", "Upload limit in GiB").Envar("UPLOAD_LIMIT").Default("2").Int64Var(&p.UploadLimitGB)
	app.Flag("cleanup.interval", "interval in seconds for cleanup").Default("60").IntVar(&p.CleanupInterval)
	app.Flag("healthcheck.interval", "interval in seconds for healthcheck").Default("2").IntVar(&p.HealthCheckInterval)
	app.Flag("healthcheck.return.gap", "time in seconds for declaring the service as healthy after successful check").Default("2s").DurationVar(&p.HealthCheckReturnGap)
	app.Flag("s3.endpoint", "address to s3 endpoint").Envar("S3_ENDPOINT").StringVar(&p.S3Endpoint)
	app.Flag("s3.access", "s3 access key").Envar("AWS_ACCESS_KEY_ID").StringVar(&p.S3AccessKey)
	app.Flag("s3.secret", "s3 secret key").Envar("AWS_SECRET_ACCESS_KEY").StringVar(&p.S3SecretKey)
	app.Flag("s3.bucket", "s3 storage bucket").Envar("S3_BUCKET").StringVar(&p.S3BucketName)
	app.Flag("s3.secure", "use tls for connection").Envar("S3_SECURE").Default("true").BoolVar(&p.S3UseSecurity)
	app.Flag("cleanup.disable", "manage object deletion process").Default("false").BoolVar(&p.DisableCleanupWorker)
	app.Flag("link.prefix", "prepending stuff for download link").Default("http").StringVar(&p.DownloadLinkPrefix)

	app.HelpFlag.Short('h')
	app.Version(version.Print(os.Args[0]))
	kingpin.MustParse(app.Parse(os.Args[1:]))

	if p.S3AccessKey == "" || p.S3SecretKey == "" || p.S3Endpoint == "" || p.S3BucketName == "" {
		fmt.Println("no s3 details given")
		os.Exit(1)
	}

	// no thread limit
	runtime.GOMAXPROCS(-1)
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

	c.logger = log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile|log.Lmsgprefix)
	c.minioClient, err = minio.New(p.S3Endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(p.S3AccessKey, p.S3SecretKey, ""),
		Secure:    p.S3UseSecurity,
		Transport: metrics.RoundTripper{},
	})
	if err != nil {
		c.logger.Println(err)
		os.Exit(1)
	}

	sentryInitError := sentry.Init(sentry.ClientOptions{
		Release:          version.Revision,
		TracesSampleRate: 1.0,
		Debug:            false,
		EnableTracing:    true,
		AttachStacktrace: true,
	})
	log.Println(sentryInitError)
	sentryHandler := sentryhttp.New(sentryhttp.Options{
		WaitForDelivery: false,
	})

	applicationRouter := mux.NewRouter()
	applicationRouter.Use(sentryHandler.Handle)
	applicationRouter.HandleFunc("/{filename}", metrics.ApiMiddleware(c.UploadHandler, c.logger, "upload")).Methods(http.MethodPut)
	applicationRouter.HandleFunc("/{id}/{filename}", metrics.ApiMiddleware(c.DownloadHandler, c.logger, "download")).Methods(http.MethodGet)
	applicationRouter.HandleFunc("/{id}/{filename}/{sum:sum}", metrics.ApiMiddleware(c.DownloadHandler, c.logger, "sum")).Methods(http.MethodGet)

	metricsRouter := mux.NewRouter()
	metricsRouter.Handle("/metrics", promhttp.Handler()).Methods(http.MethodGet)
	metricsRouter.HandleFunc("/-/ready", c.HealthCheckHandler).Methods(http.MethodGet)
	metricsRouter.HandleFunc("/-/healthy", c.HealthCheckHandler).Methods(http.MethodGet)

	// declare http applicationServer
	servers := []*http.Server{
		{
			Addr:    p.ListenAddress,
			Handler: applicationRouter,
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

	// create array of worker functions
	var workers []func(context.Context, chan<- interface{})

	// add workers to schedule array
	workers = append(workers, c.HealthCheckWorker)
	if !p.DisableCleanupWorker {
		workers = append(workers, c.CleanupWorker)
	}

	// start worker processes
	workerCount := len(workers) - 1
	for _, worker := range workers {
		go worker(ctx, done)
	}

	// wait until workers are done
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
