//go:build integration

package app_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/delivery"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestRegistration_VerifyStandardUser(t *testing.T) {
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
	database := openAppDatabase(t, databaseURL)

	privateKey, publicKeyFile := writeTestKeyPair(t)
	topic := fmt.Sprintf("iam.email-delivery-request.v1.%d", time.Now().UnixNano())
	createKafkaTopic(t, strings.Split(brokers, ","), topic)

	iamBinary := buildIAM(t)
	relayBinary := buildRelayBinary(t)

	iam := startProcess(t, iamBinary, map[string]string{
		"DATABASE_URL":                           databaseURL,
		"SERVER_ADDRESS":                         "127.0.0.1:0",
		"GRPC_ADDRESS":                           "127.0.0.1:0",
		"APP_ENV":                                "test",
		"LOG_LEVEL":                              "info",
		"EMAIL_DELIVERY_TOPIC":                   topic,
		"NOTIFICATIONS_DELIVERY_PUBLIC_KEY_FILE": publicKeyFile,
		"NOTIFICATIONS_DELIVERY_KEY_ID":          "notifications-test",
		"ALLOWED_ORIGINS":                        "https://app.example.com",
	})
	iamAddress := iam.waitForAddress(t)
	client := &http.Client{Timeout: 30 * time.Second}
	baseURL := "http://" + iamAddress
	waitUntilReady(t, client, baseURL+"/v1/health/ready", iam.output)

	relay := startProcess(t, relayBinary, map[string]string{
		"DATABASE_URL":         databaseURL,
		"KAFKA_BROKERS":        brokers,
		"EMAIL_DELIVERY_TOPIC": topic,
		"RELAY_SERVER_ADDRESS": "127.0.0.1:0",
		"RELAY_POLL_INTERVAL":  "50ms",
		"APP_ENV":              "test",
		"LOG_LEVEL":            "info",
	})
	relayAddress := relay.waitForAddress(t)
	waitUntilReady(t, client, "http://"+relayAddress+"/v1/health/ready", relay.output)

	consumerClient, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(brokers, ",")...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()),
	)
	require.NoError(t, err)
	t.Cleanup(consumerClient.Close)

	email := "New.User@Example.COM"
	password := "correct horse battery staple"
	first := postJSON(t, client, baseURL+"/v1/registrations", map[string]string{
		"email":    email,
		"password": password,
	})
	assert.Equal(t, http.StatusAccepted, first.status)
	assert.Equal(t, "application/json", first.contentType)
	assert.Equal(t, `{"status":"accepted"}`, first.body)
	assert.NotContains(t, iam.output.String(), password)

	assertUserState(t, database, "new.user@example.com", "unverified", "standard", false)
	assert.Equal(t, int64(1), countUsers(t, database))

	challenge := consumeChallenge(t, consumerClient, privateKey)
	assert.NotContains(t, string(challenge.rawRecord), "new.user@example.com")
	assert.NotContains(t, string(challenge.rawRecord), challenge.token)

	hashBeforeRetry := passwordHashFor(t, database, "new.user@example.com")
	second := postJSON(t, client, baseURL+"/v1/registrations", map[string]string{
		"email":    "new.user@example.com",
		"password": "different passphrase!",
	})
	assert.Equal(t, http.StatusAccepted, second.status)
	assert.Equal(t, first.body, second.body)
	assert.Equal(t, int64(1), countUsers(t, database))
	assert.NotEqual(t, hashBeforeRetry, passwordHashFor(t, database, "new.user@example.com"))

	superseded := consumeChallenge(t, consumerClient, privateKey)
	assert.NotEqual(t, challenge.token, superseded.token)

	invalid := postJSON(t, client, baseURL+"/v1/email-verifications", map[string]string{
		"challenge": challenge.token,
	})
	assert.Equal(t, http.StatusBadRequest, invalid.status)
	assert.Equal(t, "application/problem+json", invalid.contentType)

	malformed := postJSON(t, client, baseURL+"/v1/email-verifications", map[string]string{
		"challenge": "not-a-valid-challenge-token",
	})
	assert.Equal(t, http.StatusBadRequest, malformed.status)

	verified := postJSON(t, client, baseURL+"/v1/email-verifications", map[string]string{
		"challenge": superseded.token,
	})
	assert.Equal(t, http.StatusNoContent, verified.status)
	assertUserState(t, database, "new.user@example.com", "active", "standard", true)
	assert.Equal(t, int64(0), countAuthenticationSessions(t, database))

	reuse := postJSON(t, client, baseURL+"/v1/email-verifications", map[string]string{
		"challenge": superseded.token,
	})
	assert.Equal(t, http.StatusBadRequest, reuse.status)

	activeAgain := postJSON(t, client, baseURL+"/v1/registrations", map[string]string{
		"email":    "NEW.USER@example.com",
		"password": password,
	})
	assert.Equal(t, http.StatusAccepted, activeAgain.status)
	assert.Equal(t, first.body, activeAgain.body)
	assert.Equal(t, int64(1), countUsers(t, database))

	shortPassword := postJSON(t, client, baseURL+"/v1/registrations", map[string]string{
		"email":    "other@example.com",
		"password": "too-short",
	})
	assert.Equal(t, http.StatusBadRequest, shortPassword.status)

	assertNoAccessLevelField(t, client, baseURL+"/v1/registrations")

	expireRegister := postJSON(t, client, baseURL+"/v1/registrations", map[string]string{
		"email":    "expire@example.com",
		"password": "expire challenge!",
	})
	assert.Equal(t, http.StatusAccepted, expireRegister.status)
	expiredToken := consumeChallenge(t, consumerClient, privateKey).token
	expireActiveChallenges(t, database, "expire@example.com")
	expired := postJSON(t, client, baseURL+"/v1/email-verifications", map[string]string{
		"challenge": expiredToken,
	})
	assert.Equal(t, http.StatusBadRequest, expired.status)
	assertUserState(t, database, "expire@example.com", "unverified", "standard", false)

	var waitGroup sync.WaitGroup
	results := make(chan int, 8)
	for range 8 {
		waitGroup.Go(func() {
			response := postJSON(t, client, baseURL+"/v1/registrations", map[string]string{
				"email":    "Concurrent@Example.COM",
				"password": "concurrent password!",
			})
			results <- response.status
		})
	}
	waitGroup.Wait()
	close(results)
	for status := range results {
		assert.Equal(t, http.StatusAccepted, status)
	}
	assert.Equal(t, int64(1), countUsersByEmail(t, database, "concurrent@example.com"))

	iam.stop(t)
	relay.stop(t)
}

type processHandle struct {
	command *exec.Cmd
	output  *synchronizedBuffer
}

type consumedChallenge struct {
	token     string
	rawRecord []byte
}

func writeTestKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	encoded, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "notifications.pub.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: encoded,
	}), 0o600))
	return privateKey, path
}

func buildRelayBinary(t *testing.T) string {
	t.Helper()
	binaryPath := filepath.Join(t.TempDir(), "outbox-relay")
	command := exec.Command("go", "build", "-o", binaryPath, "../../cmd/outbox-relay")
	output, err := command.CombinedOutput()
	require.NoErrorf(t, err, "build outbox relay: %s", output)
	return binaryPath
}

func startProcess(t *testing.T, binaryPath string, environment map[string]string) *processHandle {
	t.Helper()

	output := &synchronizedBuffer{}
	command := exec.Command(binaryPath)
	command.Env = append(os.Environ(), "APP_ENV=test", "LOG_LEVEL=info")
	for name, value := range environment {
		command.Env = append(command.Env, name+"="+value)
	}
	command.Stdout = output
	command.Stderr = output
	require.NoError(t, command.Start())

	handle := &processHandle{command: command, output: output}
	t.Cleanup(func() {
		if handle.command.ProcessState == nil {
			handle.stop(t)
		}
	})
	return handle
}

func (p *processHandle) waitForAddress(t *testing.T) string {
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
	}, 15*time.Second, 50*time.Millisecond, p.output.String())
	return address
}

func (p *processHandle) stop(t *testing.T) {
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
		require.NoErrorf(t, err, "process output: %s", p.output.String())
	case <-time.After(5 * time.Second):
		_ = p.command.Process.Kill()
		t.Fatalf("process did not stop: %s", p.output.String())
	}
}

type jsonResponse struct {
	status      int
	contentType string
	body        string
}

func postJSON(
	t *testing.T,
	client *http.Client,
	endpoint string,
	body map[string]string,
) jsonResponse {
	t.Helper()
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	response, err := client.Post(endpoint, "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return jsonResponse{
		status:      response.StatusCode,
		contentType: response.Header.Get("Content-Type"),
		body:        strings.TrimSpace(string(raw)),
	}
}

func assertNoAccessLevelField(t *testing.T, client *http.Client, endpoint string) {
	t.Helper()
	payload := []byte(`{"email":"access@example.com","password":"valid password!!","access_level":"administrator"}`)
	response, err := client.Post(endpoint, "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
}

func consumeChallenge(
	t *testing.T,
	client *kgo.Client,
	privateKey *rsa.PrivateKey,
) consumedChallenge {
	t.Helper()
	return consumeDeliveryChallenge(t, client, privateKey, "verify-email")
}

func consumeDeliveryChallenge(
	t *testing.T,
	client *kgo.Client,
	privateKey *rsa.PrivateKey,
	template string,
) consumedChallenge {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	for {
		fetches := client.PollFetches(ctx)
		for _, record := range fetches.Records() {
			var event delivery.EmailDeliveryEvent
			require.NoError(t, json.Unmarshal(record.Value, &event))
			plaintext, err := delivery.DecryptEnvelope(
				privateKey,
				event.AssociatedData(),
				event.Envelope,
			)
			require.NoError(t, err)
			var payload delivery.EmailPayload
			require.NoError(t, json.Unmarshal(plaintext, &payload))
			require.Equal(t, template, payload.Template)
			require.NotEmpty(t, payload.Variables["challenge"])
			return consumedChallenge{
				token:     payload.Variables["challenge"],
				rawRecord: append([]byte(nil), record.Value...),
			}
		}
		if ctx.Err() != nil {
			t.Fatalf("consume delivery challenge: %v", ctx.Err())
		}
	}
}

func createKafkaTopic(t *testing.T, brokers []string, topic string) {
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

func openAppDatabase(t *testing.T, databaseURL string) *postgres.Database {
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

func assertUserState(
	t *testing.T,
	database *postgres.Database,
	email string,
	status string,
	accessLevel string,
	verified bool,
) {
	t.Helper()
	rows, err := database.Query(t.Context(), `
		SELECT status, access_level, email_verified_at IS NOT NULL, password_hash
		FROM users
		WHERE email = $1
	`, email)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var gotStatus, gotAccessLevel, passwordHash string
	var gotVerified bool
	require.NoError(t, rows.Scan(&gotStatus, &gotAccessLevel, &gotVerified, &passwordHash))
	assert.Equal(t, status, gotStatus)
	assert.Equal(t, accessLevel, gotAccessLevel)
	assert.Equal(t, verified, gotVerified)
	assert.True(t, strings.HasPrefix(passwordHash, "$argon2id$"))
	require.NoError(t, rows.Err())
}

func passwordHashFor(t *testing.T, database *postgres.Database, email string) string {
	t.Helper()
	rows, err := database.Query(t.Context(), `
		SELECT password_hash FROM users WHERE email = $1
	`, email)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var hash string
	require.NoError(t, rows.Scan(&hash))
	require.NoError(t, rows.Err())
	return hash
}

func expireActiveChallenges(t *testing.T, database *postgres.Database, email string) {
	t.Helper()
	tag, err := database.Exec(t.Context(), `
		UPDATE challenges
		SET expires_at = NOW() - INTERVAL '1 minute'
		WHERE consumed_at IS NULL
		  AND superseded_at IS NULL
		  AND user_id = (SELECT id FROM users WHERE email = $1)
	`, email)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected())
}

func countUsers(t *testing.T, database *postgres.Database) int64 {
	t.Helper()
	return countQuery(t, database, `SELECT COUNT(*) FROM users`)
}

func countUsersByEmail(t *testing.T, database *postgres.Database, email string) int64 {
	t.Helper()
	rows, err := database.Query(t.Context(), `SELECT COUNT(*) FROM users WHERE email = $1`, email)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var count int64
	require.NoError(t, rows.Scan(&count))
	return count
}

func countAuthenticationSessions(t *testing.T, database *postgres.Database) int64 {
	t.Helper()
	rows, err := database.Query(t.Context(), `
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name = 'authentication_sessions'
	`)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var exists int64
	require.NoError(t, rows.Scan(&exists))
	if exists == 0 {
		return 0
	}
	return countQuery(t, database, `SELECT COUNT(*) FROM authentication_sessions`)
}

func countQuery(t *testing.T, database *postgres.Database, query string) int64 {
	t.Helper()
	rows, err := database.Query(t.Context(), query)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var count int64
	require.NoError(t, rows.Scan(&count))
	require.NoError(t, rows.Err())
	return count
}
