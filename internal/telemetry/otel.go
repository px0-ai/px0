package telemetry

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

var meterProvider *metric.MeterProvider

type otelErrorHandler struct {
	mu         sync.Mutex
	warnedConn bool
}

func (h *otelErrorHandler) Handle(err error) {
	if err == nil {
		return
	}
	errStr := err.Error()
	if strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "Unavailable") || strings.Contains(errStr, "dial tcp") {
		h.mu.Lock()
		defer h.mu.Unlock()
		if !h.warnedConn {
			log.Println("warn: OpenTelemetry collector is unreachable; background metrics upload will be skipped.")
			h.warnedConn = true
		}
		return
	}
	log.Printf("otel error: %v", err)
}

// InitMetrics initializes the OpenTelemetry metric SDK and starts runtime metrics.
func InitMetrics(ctx context.Context) (func(), error) {
	otel.SetErrorHandler(&otelErrorHandler{})

	// 1. Create the OTLP exporter. It connects to the collector over gRPC.
	// By default, it respects OTEL_EXPORTER_OTLP_ENDPOINT. If not set, it defaults to localhost:4317.
	exporter, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OTLP metrics exporter: %w", err)
	}

	// 2. Define our service resource.
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "px0"
	}
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			"", // Empty schema URL allows successful merge with default resource's schema URL
			semconv.ServiceNameKey.String(serviceName),
			semconv.ServiceVersionKey.String("1.0.0"),
			attribute.String("environment", "production"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	// 3. Create the MeterProvider with a PeriodicReader.
	mp := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(exporter, metric.WithInterval(5*time.Second))), // Collect/push metrics every 5s for fast local feedback
	)

	// Set the global meter provider.
	otel.SetMeterProvider(mp)
	meterProvider = mp

	// 4. Start automatic Go runtime metrics collection.
	if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(5 * time.Second)); err != nil {
		return nil, fmt.Errorf("start Go runtime metrics: %w", err)
	}

	// 5. Return a shutdown function to gracefully flush and stop the MeterProvider.
	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := mp.Shutdown(shutdownCtx); err != nil {
			otel.Handle(err)
		}
	}

	return shutdown, nil
}
