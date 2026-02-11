package obs

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
)

func InitTracerProvider() func(context.Context) error {
	tp := trace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	return tp.Shutdown
}
