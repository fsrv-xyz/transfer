package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	_  = iota
	KB = 1 << (10 * iota)
	MB
	GB
)

const (
	LabelMethod   = "method"
	LabelEndpoint = "endpoint"
	LabelStatus   = "status"
	LabelAction   = "action"
)

const (
	namespace = "transfer"
)

var (
	BackendRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "backend_request_duration",
		Help:      "duration per backend request",
	}, []string{LabelMethod, LabelEndpoint, LabelStatus})

	ObjectSize = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "object_size_bytes",
		Help:      "Uploaded objects by size",
		Buckets:   []float64{1 * KB, 10 * KB, 100 * KB, 1 * MB, 10 * MB, 100 * MB, 300 * MB, 600 * MB, 900 * MB},
	})

	EndpointRequests = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "endpoint_requests",
		Help:      "HTTP endpoint requests",
	}, []string{LabelEndpoint})

	ObjectAction = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "object_action",
		Help:      "Actions applied to objects",
	}, []string{LabelAction})

	OperationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "operation_duration",
		Help:      "duration per endpoint",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.2, 0.4, 1, 2, 4, 8, 10, 20},
	}, []string{LabelEndpoint})
)
