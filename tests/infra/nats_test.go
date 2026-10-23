package infra

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testcontainersnats "github.com/testcontainers/testcontainers-go/modules/nats"
)

// natsImage matches the NATS version pinned in deploy/compose/docker-compose.yml
// so that the infra contract is exercised against the same build we ship.
const natsImage = "nats:2.10.29-alpine"

// skipIfNoDocker gracefully skips tests when Docker is not available.
func skipIfNoDocker(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	provider, err := testcontainers.ProviderDocker.GetProvider()
	if err != nil {
		t.Skipf("Docker not available, skipping: %v", err)
	}
	defer func() { _ = provider.Close() }()

	if err := provider.Health(ctx); err != nil {
		t.Skipf("Docker not available, skipping: %v", err)
	}
}

// startNATS boots a NATS container with JetStream enabled and registers a
// cleanup hook that terminates it.
func startNATS(t *testing.T, ctx context.Context) string {
	t.Helper()

	natsContainer, err := testcontainersnats.Run(ctx, natsImage)
	require.NoError(t, err, "failed to start NATS container")
	t.Cleanup(func() {
		_ = natsContainer.Terminate(ctx)
	})

	uri, err := natsContainer.ConnectionString(ctx)
	require.NoError(t, err, "failed to resolve NATS endpoint")

	return uri
}

func TestNATS_JetStream_PublishSubscribe(t *testing.T) {
	skipIfNoDocker(t)

	ctx := context.Background()
	streamName := "TEST_STREAM"
	subject := "test.events"
	message := []byte(`{"type":"test","payload":"hello-jetstream"}`)

	uri := startNATS(t, ctx)

	// Get NATS client connection
	nc, err := nats.Connect(uri)
	require.NoError(t, err, "failed to connect to NATS")
	defer nc.Close()

	// Verify JetStream is enabled
	js, err := nc.JetStream()
	require.NoError(t, err, "JetStream should be enabled")

	// Create stream
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     streamName,
		Subjects: []string{subject},
		Storage:  nats.FileStorage,
	})
	require.NoError(t, err, "failed to create stream")

	// Publish message
	ack, err := js.Publish(subject, message)
	require.NoError(t, err, "failed to publish message")
	assert.NotZero(t, ack.Sequence, "published message should have sequence")

	// Subscribe as JetStream consumer
	sub, err := js.SubscribeSync(subject, nats.Durable("test-consumer"), nats.AckExplicit())
	require.NoError(t, err, "failed to subscribe")

	// Receive message
	received, err := sub.NextMsg(5 * time.Second)
	require.NoError(t, err, "should receive published message")
	assert.Equal(t, message, received.Data, "message data should match")

	// Acknowledge
	err = received.Ack()
	require.NoError(t, err, "should acknowledge message")
}

func TestNATS_JetStream_Persistence(t *testing.T) {
	skipIfNoDocker(t)

	ctx := context.Background()
	streamName := "TEST_PERSIST_STREAM"
	subject := "persist.events"

	uri := startNATS(t, ctx)

	nc, err := nats.Connect(uri)
	require.NoError(t, err)
	defer nc.Close()

	js, err := nc.JetStream()
	require.NoError(t, err)

	// Create stream and publish
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     streamName,
		Subjects: []string{subject},
		Storage:  nats.FileStorage,
	})
	require.NoError(t, err)

	_, err = js.Publish(subject, []byte("persist-me"))
	require.NoError(t, err)

	// Verify message is stored in stream info
	info, err := js.StreamInfo(streamName)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), info.State.Msgs, "stream should have 1 message")
}
