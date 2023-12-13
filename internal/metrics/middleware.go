package metrics

import (
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ApiMiddleware - logging and metrics for api endpoints
func ApiMiddleware(handler http.HandlerFunc, logger *log.Logger, endpointName string) http.HandlerFunc {
	if logger == nil {
		logger = log.Default()
	}
	fn := func(w http.ResponseWriter, r *http.Request) {
		EndpointRequests.With(prometheus.Labels{LabelEndpoint: endpointName}).Inc()
		start := time.Now()

		// serve http request
		handler.ServeHTTP(w, r)
		duration := time.Since(start)
		OperationDuration.With(prometheus.Labels{LabelEndpoint: endpointName}).Observe(duration.Seconds())

		logger.Printf("%v %v %v", r.Method, r.RequestURI, duration)
	}
	return fn
}
