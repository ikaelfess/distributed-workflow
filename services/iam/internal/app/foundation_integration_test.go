//go:build integration

package app_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestFoundation_HealthAgainstPostgres(t *testing.T) {
	adminURL := os.Getenv("IAM_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("IAM_TEST_DATABASE_URL is required")
	}

	databaseURL := createTestDatabase(t, adminURL)
	migrateDatabase(t, databaseURL)

	binaryPath := buildIAM(t)
	publicKeyFile := writeTestPublicKeyFile(t)

	var processOutput synchronizedBuffer
	command := exec.Command(binaryPath)
	command.Env = append(
		os.Environ(),
		"DATABASE_URL="+databaseURL,
		"SERVER_ADDRESS=127.0.0.1:0",
		"GRPC_ADDRESS=127.0.0.1:0",
		"APP_ENV=test",
		"LOG_LEVEL=info",
		"NOTIFICATIONS_DELIVERY_PUBLIC_KEY_FILE="+publicKeyFile,
		"NOTIFICATIONS_DELIVERY_KEY_ID=notifications-test",
	)
	command.Stdout = &processOutput
	command.Stderr = &processOutput

	require.NoError(t, command.Start())
	t.Cleanup(func() {
		if command.ProcessState != nil {
			return
		}

		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("kill IAM process: %v", err)
		}
		if err := command.Wait(); err != nil {
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) {
				t.Errorf("wait for killed IAM process: %v", err)
			}
		}
	})

	serverAddress := waitForServerAddress(t, &processOutput)
	client := &http.Client{Timeout: time.Second}
	baseURL := "http://" + serverAddress
	waitUntilReady(t, client, baseURL+"/v1/health/ready", &processOutput)

	assertHealth(t, client, baseURL+"/v1/health/live", http.StatusOK, "application/json")
	assertHealth(t, client, baseURL+"/v1/health/ready", http.StatusOK, "application/json")
	assertProblem(t, client, baseURL+"/v1/unknown", http.StatusNotFound)

	require.NoError(t, command.Process.Signal(os.Interrupt))
	waitForExit(t, command, &processOutput)
}

func buildIAM(t *testing.T) string {
	t.Helper()

	binaryPath := filepath.Join(t.TempDir(), "iam")
	command := exec.Command("go", "build", "-o", binaryPath, "../../cmd/iam")
	output, err := command.CombinedOutput()
	require.NoErrorf(t, err, "build IAM: %s", output)

	return binaryPath
}

func writeTestPublicKeyFile(t *testing.T) string {
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
	return path
}

func waitForServerAddress(t *testing.T, processOutput *synchronizedBuffer) string {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for line := range strings.Lines(processOutput.String()) {
			var event struct {
				Address string `json:"addr"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue
			}
			if event.Message == "http server started" && event.Address != "" {
				return event.Address
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("IAM did not start: %s", processOutput.String())
	return ""
}

func waitUntilReady(
	t *testing.T,
	client *http.Client,
	endpoint string,
	processOutput *synchronizedBuffer,
) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(endpoint)
		if err == nil {
			if closeErr := response.Body.Close(); closeErr != nil {
				t.Fatalf("close readiness response: %v", closeErr)
			}
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("IAM did not become ready: %s", processOutput.String())
}

func waitForExit(t *testing.T, command *exec.Cmd, processOutput *synchronizedBuffer) {
	t.Helper()

	waitResult := make(chan error, 1)
	go func() {
		waitResult <- command.Wait()
	}()

	select {
	case err := <-waitResult:
		require.NoErrorf(t, err, "IAM process output: %s", processOutput.String())
	case <-time.After(5 * time.Second):
		if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("kill IAM after shutdown timeout: %v", err)
		}
		t.Fatalf("IAM did not stop gracefully: %s", processOutput.String())
	}
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

	databaseName := fmt.Sprintf("iam_test_%d", time.Now().UnixNano())
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

	assertUserSchema(t, database)
}

func assertUserSchema(t *testing.T, database *sql.DB) {
	t.Helper()

	var accessLevelDefault string
	err := database.QueryRowContext(t.Context(), `
		SELECT column_default
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = 'users'
		  AND column_name = 'access_level'
	`).Scan(&accessLevelDefault)
	require.NoError(t, err)
	assert.Equal(t, "'standard'::text", accessLevelDefault)

	var statusDefault string
	err = database.QueryRowContext(t.Context(), `
		SELECT column_default
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name = 'users'
		  AND column_name = 'status'
	`).Scan(&statusDefault)
	require.NoError(t, err)
	assert.Equal(t, "'unverified'::text", statusDefault)
}

func assertProblem(t *testing.T, client *http.Client, endpoint string, expectedStatus int) {
	t.Helper()

	response, err := client.Get(endpoint)
	require.NoError(t, err)
	defer response.Body.Close()

	assert.Equal(t, expectedStatus, response.StatusCode)
	assert.Equal(t, "application/problem+json", response.Header.Get("Content-Type"))

	var problem struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&problem))
	assert.NotEmpty(t, problem.Type)
	assert.NotEmpty(t, problem.Title)
	assert.Equal(t, expectedStatus, problem.Status)
	assert.NotEmpty(t, problem.Detail)
}

func assertHealth(
	t *testing.T,
	client *http.Client,
	endpoint string,
	expectedStatus int,
	expectedContentType string,
) {
	t.Helper()

	response, err := client.Get(endpoint)
	require.NoError(t, err)
	defer response.Body.Close()

	assert.Equal(t, expectedStatus, response.StatusCode)
	assert.Equal(t, expectedContentType, response.Header.Get("Content-Type"))

	if expectedStatus == http.StatusOK {
		var health struct {
			Status string `json:"status"`
		}
		require.NoError(t, json.NewDecoder(response.Body).Decode(&health))
		assert.Equal(t, "ok", health.Status)
		return
	}

	var problem struct {
		Status int `json:"status"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&problem))
	assert.Equal(t, expectedStatus, problem.Status)
}
