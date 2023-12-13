package metrics

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/prometheus/client_golang/prometheus"
)

type RoundTripper struct{}

func (t RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()

	span := sentry.SpanFromContext(req.Context())

	if span != nil {
		child := span.StartChild(fmt.Sprintf("%s %s", req.Method, req.URL.String()))
		child.SetData("http.method", req.Method)
		child.SetData("http.url", req.URL.String())
		child.SetData("http.content_length", strconv.FormatInt(req.ContentLength, 10))
		defer func() {
			if child != nil {
				child.Finish()
			}
		}()
	}

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	BackendRequestDuration.With(prometheus.Labels{
		LabelMethod:   req.Method,
		LabelEndpoint: req.URL.Host,
		LabelStatus:   strconv.Itoa(resp.StatusCode),
	}).Observe(time.Since(start).Seconds())

	return resp, err
}
