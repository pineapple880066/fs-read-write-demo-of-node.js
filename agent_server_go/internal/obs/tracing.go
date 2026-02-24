package obs

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
)

func InitTracerProvider() func(context.Context) error {
	// 当前是最小 tracer provider 骨架，后续可接 Jaeger/OTLP exporter
	tp := trace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	return tp.Shutdown
}
