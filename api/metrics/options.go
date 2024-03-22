package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/redpanda-data/common-go/api/interceptor"
)

// Option is a functional option type that allows us to configure the Prometheus type.
type Option func(*Prometheus)

// WithRegistry allows to provide a prometheus registry. If not provided, the default
// global Prometheus registry will be used.
func WithRegistry(registry prometheus.Registerer) Option {
	return func(p *Prometheus) {
		p.registry = registry
	}
}

// WithConstLabels can be used to attach static labels to the exported metrics.
func WithConstLabels(labels map[string]string) Option {
	return func(p *Prometheus) {
		p.constLabels = labels
	}
}

// WithDynamicLabel allows adding dynamic labels. The key is the label key
// and the valueGetter is called for every finished request. Thus, the
// valueGetter must be safe to be called concurrently. You can add multiple
// dynamic labels.
func WithDynamicLabel(key string, valueGetter func(context.Context, *interceptor.RequestMetadata) string) Option {
	return func(p *Prometheus) {
		p.dynamicLabelFns[key] = valueGetter
	}
}

// WithMetricsNamespace can be used to set the metrics namespace for all exported metrics.
func WithMetricsNamespace(metricsNamespace string) Option {
	return func(p *Prometheus) {
		p.metricsNamespace = metricsNamespace
	}
}
