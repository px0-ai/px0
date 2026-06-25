package middleware

import (
	"context"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter                 metric.Meter
	requestCounter        metric.Int64Counter
	durationHistogram      metric.Float64Histogram
	activeRequestsCounter  metric.Int64UpDownCounter
)

func init() {
	// OpenTelemetry's global provider is lazy-initialized.
	// Instruments registered here will automatically hook into the real meter provider once initialized.
	meter = otel.GetMeterProvider().Meter("github.com/arpitbhayani/px0/internal/middleware")

	var err error
	requestCounter, err = meter.Int64Counter(
		"http_server_requests_total",
		metric.WithDescription("Total number of HTTP requests processed"),
		metric.WithUnit("1"),
	)
	if err != nil {
		otel.Handle(err)
	}

	durationHistogram, err = meter.Float64Histogram(
		"http_server_duration_seconds",
		metric.WithDescription("Latency of HTTP requests in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		otel.Handle(err)
	}

	activeRequestsCounter, err = meter.Int64UpDownCounter(
		"http_server_active_requests",
		metric.WithDescription("Number of active/in-flight HTTP requests"),
		metric.WithUnit("1"),
	)
	if err != nil {
		otel.Handle(err)
	}
}

// Metrics returns a Fiber middleware for gathering HTTP request metrics.
func Metrics() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		ctx := c.UserContext()
		if ctx == nil {
			ctx = context.Background()
		}

		routePath := "unmatched"
		if route := c.Route(); route != nil && route.Path != "" {
			routePath = route.Path
		}

		method := c.Method()

		// Track active requests
		activeAttrs := attribute.NewSet(
			attribute.String("http.method", method),
			attribute.String("http.route", routePath),
		)
		activeRequestsCounter.Add(ctx, 1, metric.WithAttributeSet(activeAttrs))
		defer activeRequestsCounter.Add(ctx, -1, metric.WithAttributeSet(activeAttrs))

		// Execute request handler
		err := c.Next()

		// Determine status code
		statusCode := fiber.StatusOK
		if err != nil {
			if e, ok := err.(*fiber.Error); ok {
				statusCode = e.Code
			} else {
				statusCode = fiber.StatusInternalServerError
			}
		} else {
			statusCode = c.Response().StatusCode()
		}

		duration := time.Since(start).Seconds()

		attrs := attribute.NewSet(
			attribute.String("http.method", method),
			attribute.String("http.route", routePath),
			attribute.String("http.status_code", strconv.Itoa(statusCode)),
		)

		requestCounter.Add(ctx, 1, metric.WithAttributeSet(attrs))
		durationHistogram.Record(ctx, duration, metric.WithAttributeSet(attrs))

		return err
	}
}
