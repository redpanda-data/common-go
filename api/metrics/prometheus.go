// Package metrics provides functionality to instrument your API servers
// using metrics such as Prometheus.
package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/redpanda-data/common-go/api/interceptor"
)

// Prometheus can be used to instrument your API server and collect
// a standard set of metrics on the server. It is designed to work
// alongside the interceptor.Observer middleware.
type Prometheus struct {
	registry         prometheus.Registerer
	constLabels      prometheus.Labels
	metricsNamespace string
	// dynamicLabelFns can be used to dynamically add label partitions. The key
	// is the label key and the according function must return the value. The
	// function must be safe to be called concurrently.
	dynamicLabelFns map[string]func(context.Context, *interceptor.RequestMetadata) string

	requestDuration *prometheus.HistogramVec
}

// NewPrometheus creates a new Prometheus instance. If it fails to register
// a collector an error will be returned.
func NewPrometheus(opts ...Option) (*Prometheus, error) {
	p := &Prometheus{
		dynamicLabelFns: make(map[string]func(context.Context, *interceptor.RequestMetadata) string),
	}
	for _, o := range opts {
		o(p)
	}

	labelKeys := []string{"procedure", "status"}
	for key := range p.dynamicLabelFns {
		labelKeys = append(labelKeys, key)
	}

	requestDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace:   p.metricsNamespace,
		Name:        "request_duration_seconds",
		Help:        "Time (in seconds) spent in serving requests",
		ConstLabels: p.constLabels,
		Buckets:     prometheus.DefBuckets,
	}, labelKeys)

	if p.registry == nil {
		p.registry = prometheus.DefaultRegisterer
	}
	if err := p.registry.Register(requestDuration); err != nil {
		return nil, err
	}
	p.requestDuration = requestDuration

	return p, nil
}

// ObserverAdapter returns the callback function for an interceptor.Observer.
// Pass this function to an observer to collect metrics for your API server.
func (p *Prometheus) ObserverAdapter() func(context.Context, *interceptor.RequestMetadata) {
	return func(ctx context.Context, rm *interceptor.RequestMetadata) {
		labels := map[string]string{
			"procedure": rm.Procedure(),
			"status":    rm.StatusCode(),
		}
		for key, fn := range p.dynamicLabelFns {
			labels[key] = fn(ctx, rm)
		}
		p.requestDuration.With(labels).Observe(rm.Duration().Seconds())
	}
}
