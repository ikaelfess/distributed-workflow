//go:build integration

package outbox_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/delivery"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/kafka"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/outbox"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type relayProcess struct {
	command *exec.Cmd
	output  *synchronizedBuffer
}

type deduplicatingConsumer struct {
	privateKey *rsa.PrivateKey
	seen       map[string]struct{}
	deliveries []delivery.EmailPayload
}

func TestRelay_EncryptedDeliverySurvivesBrokerInterruption(t *testing.T) {
	adminURL := os.Getenv("IAM_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("IAM_TEST_DATABASE_URL is required")
	}
	brokers := strings.TrimSpace(os.Getenv("IAM_TEST_KAFKA_BROKERS"))
	if brokers == "" {
		t.Skip("IAM_TEST_KAFKA_BROKERS is required")
	}

	databaseURL := createTestDatabase(t, adminURL)
	migrateDatabase(t, databaseURL)
	database := openDatabase(t, databaseURL)
	store, err := outbox.NewStore(database)
	require.NoError(t, err)

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	encryptor, err := delivery.NewEnvelopeEncryptor(
		"notifications-integration",
		&privateKey.PublicKey,
	)
	require.NoError(t, err)

	challenge := "raw-verification-challenge"
	recipient := "integration@example.com"
	topic := fmt.Sprintf("iam.email-delivery-request.v1.%d", time.Now().UnixNano())
	createTopic(t, strings.Split(brokers, ","), topic)
	event, err := delivery.NewEmailDeliveryEvent(time.Now(), delivery.EmailPayload{
		Recipient: recipient,
		Template:  "verify-email",
		Variables: map[string]string{"challenge": challenge},
	}, encryptor)
	require.NoError(t, err)

	assertRollbackIsAtomic(t, database, store, topic, encryptor)
	commitUserAndEvent(t, database, store, topic, event)
	assertEncryptedAtRest(t, database, event.ID, recipient, challenge)

	binaryPath := buildRelay(t)
	failedRelay := startRelay(t, binaryPath, map[string]string{
		"DATABASE_URL":           databaseURL,
		"KAFKA_BROKERS":          "127.0.0.1:1",
		"EMAIL_DELIVERY_TOPIC":   topic,
		"RELAY_POLL_INTERVAL":    "50ms",
		"RELAY_RETRY_DELAY":      "1s",
		"KAFKA_DELIVERY_TIMEOUT": "1s",
	})
	failedAddress := failedRelay.waitForAddress(t)
	assertReadiness(t, failedAddress, http.StatusServiceUnavailable)
	waitForRetry(t, database, event.ID)
	failedRelay.stop(t)
	assert.NotContains(t, failedRelay.output.String(), recipient)
	assert.NotContains(t, failedRelay.output.String(), challenge)

	consumerClient, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(brokers, ",")...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()),
	)
	require.NoError(t, err)
	t.Cleanup(consumerClient.Close)

	workingRelay := startRelay(t, binaryPath, map[string]string{
		"DATABASE_URL":         databaseURL,
		"KAFKA_BROKERS":        brokers,
		"EMAIL_DELIVERY_TOPIC": topic,
		"RELAY_POLL_INTERVAL":  "50ms",
		"RELAY_RETRY_DELAY":    "100ms",
	})
	workingAddress := workingRelay.waitForAddress(t)
	assertReadiness(t, workingAddress, http.StatusOK)

	record := consumeRecord(t, consumerClient, workingRelay.output)
	assert.Equal(t, event.ID, string(record.Key))
	assert.NotContains(t, string(record.Value), recipient)
	assert.NotContains(t, string(record.Value), challenge)

	testConsumer := &deduplicatingConsumer{
		privateKey: privateKey,
		seen:       make(map[string]struct{}),
	}
	accepted, err := testConsumer.consume(record)
	require.NoError(t, err)
	assert.True(t, accepted)
	require.Len(t, testConsumer.deliveries, 1)
	assert.Equal(t, recipient, testConsumer.deliveries[0].Recipient)
	assert.Equal(t, challenge, testConsumer.deliveries[0].Variables["challenge"])

	waitForOutboxDeletion(t, database, event.ID)

	duplicatePublisher, err := kafka.NewPublisher(strings.Split(brokers, ","), 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(duplicatePublisher.Close)
	require.NoError(t, duplicatePublisher.Publish(t.Context(), topic, event.ID, record.Value))

	duplicate := consumeRecord(t, consumerClient, workingRelay.output)
	accepted, err = testConsumer.consume(duplicate)
	require.NoError(t, err)
	assert.False(t, accepted)
	assert.Len(t, testConsumer.deliveries, 1)

	workingRelay.stop(t)
	assert.NotContains(t, workingRelay.output.String(), recipient)
	assert.NotContains(t, workingRelay.output.String(), challenge)
}

func (c *deduplicatingConsumer) consume(record *kgo.Record) (bool, error) {
	var event delivery.EmailDeliveryEvent
	if err := json.Unmarshal(record.Value, &event); err != nil {
		return false, fmt.Errorf("decode delivery event: %w", err)
	}
	if _, exists := c.seen[event.ID]; exists {
		return false, nil
	}

	plaintext, err := delivery.DecryptEnvelope(
		c.privateKey,
		event.AssociatedData(),
		event.Envelope,
	)
	if err != nil {
		return false, fmt.Errorf("decrypt delivery event: %w", err)
	}

	var payload delivery.EmailPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return false, fmt.Errorf("decode delivery payload: %w", err)
	}

	c.seen[event.ID] = struct{}{}
	c.deliveries = append(c.deliveries, payload)
	return true, nil
}

func assertRollbackIsAtomic(
	t *testing.T,
	database *postgres.Database,
	store *outbox.Store,
	topic string,
	encryptor *delivery.EnvelopeEncryptor,
) {
	t.Helper()

	event, err := delivery.NewEmailDeliveryEvent(time.Now(), delivery.EmailPayload{
		Recipient: "rollback@example.com",
		Template:  "verify-email",
		Variables: map[string]string{"challenge": "rollback-secret"},
	}, encryptor)
	require.NoError(t, err)

	tx, err := database.Begin(t.Context())
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `
		INSERT INTO users (email, password_hash)
		VALUES ($1, $2)
	`, "rollback@example.com", "argon2id-placeholder")
	require.NoError(t, err)
	require.NoError(t, store.EnqueueEmailDelivery(t.Context(), tx, topic, event))
	require.NoError(t, tx.Rollback(t.Context()))

	assert.Equal(t, int64(0), countRows(t, database, "outbox_events", event.ID))
	assert.Equal(t, int64(0), countRows(t, database, "users", "rollback@example.com"))
}

func commitUserAndEvent(
	t *testing.T,
	database *postgres.Database,
	store *outbox.Store,
	topic string,
	event delivery.EmailDeliveryEvent,
) {
	t.Helper()

	tx, err := database.Begin(t.Context())
	require.NoError(t, err)
	defer func() {
		rollbackErr := tx.Rollback(context.Background())
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			t.Errorf("rollback delivery transaction: %v", rollbackErr)
		}
	}()

	_, err = tx.Exec(t.Context(), `
		INSERT INTO users (email, password_hash)
		VALUES ($1, $2)
	`, "integration@example.com", "argon2id-placeholder")
	require.NoError(t, err)
	require.NoError(t, store.EnqueueEmailDelivery(t.Context(), tx, topic, event))
	require.NoError(t, tx.Commit(t.Context()))
}

func assertEncryptedAtRest(
	t *testing.T,
	database *postgres.Database,
	eventID string,
	recipient string,
	challenge string,
) {
	t.Helper()

	rows, err := database.Query(t.Context(), `
		SELECT payload::text
		FROM outbox_events
		WHERE id = $1
	`, eventID)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())

	var payload string
	require.NoError(t, rows.Scan(&payload))
	assert.NotContains(t, payload, recipient)
	assert.NotContains(t, payload, challenge)
	require.NoError(t, rows.Err())
}

func waitForRetry(t *testing.T, database *postgres.Database, eventID string) {
	t.Helper()

	require.Eventually(t, func() bool {
		rows, err := database.Query(t.Context(), `
			SELECT attempts, claim_token IS NULL
			FROM outbox_events
			WHERE id = $1
		`, eventID)
		if err != nil {
			return false
		}
		defer rows.Close()
		if !rows.Next() {
			return false
		}

		var attempts int
		var released bool
		if err := rows.Scan(&attempts, &released); err != nil {
			return false
		}
		return attempts >= 1 && released
	}, 10*time.Second, 50*time.Millisecond)
}

func waitForOutboxDeletion(t *testing.T, database *postgres.Database, eventID string) {
	t.Helper()

	require.Eventually(t, func() bool {
		return countRows(t, database, "outbox_events", eventID) == 0
	}, 10*time.Second, 50*time.Millisecond)
}

func countRows(
	t *testing.T,
	database *postgres.Database,
	table string,
	identifier string,
) int64 {
	t.Helper()

	var query string
	switch table {
	case "outbox_events":
		query = "SELECT COUNT(*) FROM outbox_events WHERE id = $1"
	case "users":
		query = "SELECT COUNT(*) FROM users WHERE email = $1"
	default:
		t.Fatalf("unsupported count table %q", table)
	}

	rows, err := database.Query(t.Context(), query, identifier)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())

	var count int64
	require.NoError(t, rows.Scan(&count))
	require.NoError(t, rows.Err())
	return count
}

func consumeRecord(
	t *testing.T,
	client *kgo.Client,
	processOutput *synchronizedBuffer,
) *kgo.Record {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	for {
		fetches := client.PollFetches(ctx)
		records := fetches.Records()
		if len(records) > 0 {
			return records[0]
		}
		if ctx.Err() != nil {
			t.Fatalf("consume kafka record: %v; relay output: %s", ctx.Err(), processOutput.String())
		}
	}
}

func createTopic(t *testing.T, brokers []string, topic string) {
	t.Helper()

	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	require.NoError(t, err)
	t.Cleanup(client.Close)
	require.NoError(t, client.Ping(t.Context()))

	responses, err := kadm.NewClient(client).CreateTopics(t.Context(), 1, 1, nil, topic)
	require.NoError(t, err)
	response, exists := responses[topic]
	require.True(t, exists)
	require.NoError(t, response.Err)
}

func assertReadiness(t *testing.T, address string, expectedStatus int) {
	t.Helper()

	client := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		response, err := client.Get("http://" + address + "/v1/health/ready")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == expectedStatus
	}, 10*time.Second, 50*time.Millisecond)
}

func buildRelay(t *testing.T) string {
	t.Helper()

	binaryPath := filepath.Join(t.TempDir(), "outbox-relay")
	command := exec.Command("go", "build", "-o", binaryPath, "../../cmd/outbox-relay")
	output, err := command.CombinedOutput()
	require.NoErrorf(t, err, "build outbox relay: %s", output)
	return binaryPath
}

func startRelay(t *testing.T, binaryPath string, environment map[string]string) *relayProcess {
	t.Helper()

	output := &synchronizedBuffer{}
	command := exec.Command(binaryPath)
	command.Env = append(os.Environ(),
		"RELAY_SERVER_ADDRESS=127.0.0.1:0",
		"APP_ENV=test",
		"LOG_LEVEL=info",
	)
	for name, value := range environment {
		command.Env = append(command.Env, name+"="+value)
	}
	command.Stdout = output
	command.Stderr = output
	require.NoError(t, command.Start())

	process := &relayProcess{command: command, output: output}
	t.Cleanup(func() {
		if process.command.ProcessState == nil {
			process.stop(t)
		}
	})
	return process
}

func (p *relayProcess) waitForAddress(t *testing.T) string {
	t.Helper()

	var address string
	require.Eventually(t, func() bool {
		for line := range strings.Lines(p.output.String()) {
			var event struct {
				Address string `json:"addr"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue
			}
			if event.Message == "http server started" && event.Address != "" {
				address = event.Address
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond, p.output.String())
	return address
}

func (p *relayProcess) stop(t *testing.T) {
	t.Helper()
	if p.command.ProcessState != nil {
		return
	}

	require.NoError(t, p.command.Process.Signal(os.Interrupt))
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- p.command.Wait()
	}()

	select {
	case err := <-waitResult:
		require.NoErrorf(t, err, "relay output: %s", p.output.String())
	case <-time.After(5 * time.Second):
		if err := p.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("kill relay after timeout: %v", err)
		}
		t.Fatalf("relay did not stop gracefully: %s", p.output.String())
	}
}

func openDatabase(t *testing.T, databaseURL string) *postgres.Database {
	t.Helper()

	database, err := postgres.Open(t.Context(), postgres.Config{
		URL:               databaseURL,
		MaxConns:          5,
		MinConns:          1,
		MaxConnLifetime:   time.Minute,
		MaxConnIdleTime:   time.Minute,
		HealthCheckPeriod: time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(database.Close)
	return database
}

func createTestDatabase(t *testing.T, adminURL string) string {
	t.Helper()

	parsedURL, err := url.Parse(adminURL)
	require.NoError(t, err)
	adminConfig, err := pgx.ParseConfig(adminURL)
	require.NoError(t, err)
	adminConnection, err := pgx.ConnectConfig(t.Context(), adminConfig)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, adminConnection.Close(context.Background()))
	})

	databaseName := fmt.Sprintf("iam_outbox_test_%d", time.Now().UnixNano())
	_, err = adminConnection.Exec(
		t.Context(),
		"CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize(),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := adminConnection.Exec(
			context.Background(),
			"DROP DATABASE IF EXISTS "+pgx.Identifier{databaseName}.Sanitize()+" WITH (FORCE)",
		)
		require.NoError(t, cleanupErr)
	})

	parsedURL.Path = "/" + databaseName
	return parsedURL.String()
}

func migrateDatabase(t *testing.T, databaseURL string) {
	t.Helper()

	pgxConfig, err := pgx.ParseConfig(databaseURL)
	require.NoError(t, err)
	database := stdlib.OpenDB(*pgxConfig)
	t.Cleanup(func() {
		require.NoError(t, database.Close())
	})

	require.NoError(t, database.PingContext(t.Context()))
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpContext(t.Context(), database, "../../migrations"))
	assertOutboxSchema(t, database)
}

func assertOutboxSchema(t *testing.T, database *sql.DB) {
	t.Helper()

	var exists bool
	err := database.QueryRowContext(t.Context(), `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = 'public'
			  AND table_name = 'outbox_events'
		)
	`).Scan(&exists)
	require.NoError(t, err)
	assert.True(t, exists)
}
