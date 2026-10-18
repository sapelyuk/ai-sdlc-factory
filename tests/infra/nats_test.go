package infra

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
)

// skipIfNoDocker gracefully skips tests when Docker is not available.
func skipIfNoDocker(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	_, err := testcontainers.PingContainer(ctx)
	if err != nil {
		t.Skipf("Docker not available, skipping: %v", err)
	}
}

func TestNATS_JetStream_PublishSubscribe(t *testing.T) {
	skipIfNoDocker(t)

	ctx := context.Background()
	streamName := "TEST_STREAM"
	subject := "test.events"
	message := []byte(`{"type":"test","payload":"hello-jetstream"}`)

	// Start NATS container with JetStream enabled (default in nats module)
	natsContainer, err := nats.Run(ctx)
	require.NoError(t, err, "failed to start NATS container")
	t.Cleanup(func() {
		_ = natsContainer.Terminate(ctx)
	})

	// Get NATS client connection
	nc, err := nats.Connect(natsContainer.Endpoint(ctx, "nats"))
	require.NoError(t, err, "failed to connect to NATS")
	defer nc.Close()

	// Verify JetStream is enabled
	js, err := nc.JetStream()
	require.NoError(t, err, "JetStream should be enabled")

	// Create stream
	_, err = js.AddStream(ctx, &nats.StreamConfig{
		Name:     streamName,
		Subjects: []string{subject},
		Storage:  nats.FileStorage,
	})
	require.NoError(t, err, "failed to create stream")

	// Publish message
	msg, err := js.Publish(ctx, subject, message)
	require.NoError(t, err, "failed to publish message")
	assert.NotZero(t, msg.Sequence, "published message should have sequence")

	// Subscribe as JetStream consumer
	sub, err := js.SubscribeSync(subject, nats.Durable("test-consumer"), nats.AckExplicit())
	require.NoError(t, err, "failed to subscribe")

	// Receive message
	msg, err = sub.NextMsg(5 * time.Second)
	require.NoError(t, err, "should receive published message")
	assert.Equal(t, message, msg.Data, "message data should match")

	// Acknowledge
	err = msg.Ack()
	require.NoError(t, err, "should acknowledge message")
}

func TestNATS_JetStream_Persistence(t *testing.T) {
	skipIfNoDocker(t)

	ctx := context.Background()
	streamName := "TEST_PERSIST_STREAM"
	subject := "persist.events"

	// Start NATS container
	natsContainer, err := nats.Run(ctx)
	require.NoError(t, err, "failed to start NATS container")
	t.Cleanup(func() {
		_ = natsContainer.Terminate(ctx)
	})

	nc, err := nats.Connect(natsContainer.Endpoint(ctx, "nats"))
	require.NoError(t, err)
	defer nc.Close()

	js, err := nc.JetStream()
	require.NoError(t, err)

	// Create stream and publish
	_, err = js.AddStream(ctx, &nats.StreamConfig{
		Name:     streamName,
		Subjects: []string{subject},
		Storage:  nats.FileStorage,
	})
	require.NoError(t, err)

	_, err = js.Publish(ctx, subject, []byte("persist-me"))
	require.NoError(t, err)

	// Verify message is stored in stream info
	info, err := js.StreamInfo(streamName)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), info.State.Messages, "stream should have 1 message")
}
