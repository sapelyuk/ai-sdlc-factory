package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExporterEndpoint(t *testing.T) {
	tests := []struct {
		name         string
		endpoint     string
		insecureEnv  string
		wantEndpoint string
		wantInsecure bool
	}{
		{
			name:         "default endpoint",
			wantEndpoint: DefaultEndpoint,
			wantInsecure: true,
		},
		{
			name:         "plain host and port",
			endpoint:     "collector.internal:4317",
			wantEndpoint: "collector.internal:4317",
			wantInsecure: true,
		},
		{
			name:         "http scheme",
			endpoint:     "http://collector.internal:4317",
			wantEndpoint: "collector.internal:4317",
			wantInsecure: true,
		},
		{
			name:         "https scheme",
			endpoint:     "https://collector.internal:4317",
			wantEndpoint: "collector.internal:4317",
			wantInsecure: false,
		},
		{
			name:         "explicit insecure override",
			endpoint:     "https://collector.internal:4317",
			insecureEnv:  "true",
			wantEndpoint: "collector.internal:4317",
			wantInsecure: true,
		},
		{
			name:         "explicit tls override",
			endpoint:     "collector.internal:4317",
			insecureEnv:  "false",
			wantEndpoint: "collector.internal:4317",
			wantInsecure: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", test.endpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", test.insecureEnv)

			endpoint, insecure := exporterEndpoint()
			require.Equal(t, test.wantEndpoint, endpoint)
			require.Equal(t, test.wantInsecure, insecure)
		})
	}
}

func TestInitTracerShutsDown(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	shutdown, err := InitTracer(context.Background(), "api-server-test")
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	require.NoError(t, shutdown(context.Background()))
}
