//go:build integration

package app_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/httpapi"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestEnvoyTrustBoundary(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is required for the Envoy suite")
	}

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

	privateKey, publicKeyFile := writeTestKeyPair(t)
	topic := fmt.Sprintf("iam.email-delivery-request.v1.%d", time.Now().UnixNano())
	createKafkaTopic(t, strings.Split(brokers, ","), topic)

	iamBinary := buildIAM(t)
	relayBinary := buildRelayBinary(t)
	echoBinary := buildProtectedEcho(t)

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
	iamHTTP := iam.waitForAddress(t)
	iamGRPC := iam.waitForGRPCAddress(t)

	echo := startProcess(t, echoBinary, map[string]string{
		"LISTEN_ADDRESS": "127.0.0.1:0",
	})
	echoAddr := waitForEchoAddress(t, echo)

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

	client := &http.Client{Timeout: 30 * time.Second}
	waitUntilReady(t, client, "http://"+iamHTTP+"/v1/health/ready", iam.output)
	waitUntilReady(t, client, "http://"+relayAddress+"/v1/health/ready", relay.output)

	gatewayPort := freeTCPPort(t)
	configPath := writeEnvoyConfigFromTemplate(
		t,
		filepath.Join(repoRoot(t), "infra", "envoy", "envoy.local.yaml"),
		gatewayPort,
		iamHTTP,
		iamGRPC,
		echoAddr,
		"",
	)
	assertEnvoyConfigHasAuthzDeadline(t, configPath)

	envoy := startEnvoyContainer(t, configPath, gatewayPort, "")
	gatewayBase := fmt.Sprintf("http://127.0.0.1:%d", gatewayPort)
	waitUntilReady(t, client, gatewayBase+"/v1/health/ready", envoy.output)

	consumerClient, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(brokers, ",")...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()),
	)
	require.NoError(t, err)
	t.Cleanup(consumerClient.Close)

	email := "envoy.user@example.com"
	password := "correct horse battery staple"
	require.Equal(t, http.StatusAccepted, postJSON(t, client, gatewayBase+"/v1/registrations", map[string]string{
		"email":    email,
		"password": password,
	}).status)
	challenge := consumeChallenge(t, consumerClient, privateKey)
	require.Equal(t, http.StatusNoContent, postJSON(t, client, gatewayBase+"/v1/email-verifications", map[string]string{
		"challenge": challenge.token,
	}).status)

	login := postLogin(t, client, gatewayBase+"/v1/login", map[string]string{
		"email":    email,
		"password": password,
	})
	require.Equal(t, http.StatusNoContent, login.status)
	require.NotEmpty(t, login.accessToken)

	live := getGateway(t, client, gatewayBase+"/v1/health/live", nil)
	require.Equal(t, http.StatusOK, live.status)

	unauth := getGateway(t, client, gatewayBase+"/echo", nil)
	assert.Equal(t, http.StatusUnauthorized, unauth.status)

	forged := getGateway(t, client, gatewayBase+"/echo", map[string]string{
		"Cookie":                               httpapi.AccessTokenCookieName + "=" + login.accessToken,
		identity.HeaderUserID:                  "forged-user",
		identity.HeaderSubjectKind:             "forged-kind",
		identity.HeaderAccessLevel:             "administrator",
		identity.HeaderAuthenticationSessionID: "forged-session",
		identity.HeaderAccessTokenExpiresAt:    "1999-01-01T00:00:00Z",
	})
	require.Equal(t, http.StatusOK, forged.status, forged.body)
	var echoBody struct {
		Path    string            `json:"path"`
		Headers map[string]string `json:"headers"`
	}
	require.NoError(t, json.Unmarshal([]byte(forged.body), &echoBody), forged.body)
	require.Equal(t, "/echo", echoBody.Path, forged.body)
	require.NotEmpty(t, echoBody.Headers, forged.body)
	assert.NotEqual(t, "forged-user", echoBody.Headers[identity.HeaderUserID], forged.body)
	assert.Equal(t, identity.SubjectKindUser, echoBody.Headers[identity.HeaderSubjectKind], forged.body)
	assert.Equal(t, identity.AccessLevelStandard, echoBody.Headers[identity.HeaderAccessLevel], forged.body)
	assert.NotEqual(t, "forged-session", echoBody.Headers[identity.HeaderAuthenticationSessionID], forged.body)
	assert.NotEqual(t, "1999-01-01T00:00:00Z", echoBody.Headers[identity.HeaderAccessTokenExpiresAt], forged.body)
	assert.NotContains(t, iam.output.String(), login.accessToken)
	assert.NotContains(t, envoy.output.String(), login.accessToken)

	sessions := getGateway(t, client, gatewayBase+"/v1/authentication-sessions", map[string]string{
		"Cookie": httpapi.AccessTokenCookieName + "=" + login.accessToken,
	})
	require.Equal(t, http.StatusOK, sessions.status)
	assert.Contains(t, sessions.body, `"authentication_sessions"`)

	iam.stop(t)
	require.Eventually(t, func() bool {
		response := getGateway(t, client, gatewayBase+"/echo", map[string]string{
			"Cookie": httpapi.AccessTokenCookieName + "=" + login.accessToken,
		})
		return response.status == http.StatusServiceUnavailable
	}, 10*time.Second, 200*time.Millisecond)

	relay.stop(t)
	echo.stop(t)
	envoy.stop(t)
}

func TestEnvoyMTLSTrustBoundary(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is required for the Envoy suite")
	}
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl is required for the mTLS suite")
	}

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
	privateKey, publicKeyFile := writeTestKeyPair(t)
	certDir := generateTestCerts(t)
	topic := fmt.Sprintf("iam.email-delivery-request.v1.mtls.%d", time.Now().UnixNano())
	createKafkaTopic(t, strings.Split(brokers, ","), topic)

	iamBinary := buildIAM(t)
	relayBinary := buildRelayBinary(t)
	echoBinary := buildProtectedEcho(t)

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
		"GRPC_TLS_CERT_FILE":                     filepath.Join(certDir, "iam.crt"),
		"GRPC_TLS_KEY_FILE":                      filepath.Join(certDir, "iam.key"),
		"GRPC_TLS_CLIENT_CA_FILE":                filepath.Join(certDir, "ca.crt"),
	})
	iamHTTP := iam.waitForAddress(t)
	iamGRPC := iam.waitForGRPCAddress(t)

	echo := startProcess(t, echoBinary, map[string]string{
		"LISTEN_ADDRESS":     "127.0.0.1:0",
		"TLS_CERT_FILE":      filepath.Join(certDir, "echo.crt"),
		"TLS_KEY_FILE":       filepath.Join(certDir, "echo.key"),
		"TLS_CLIENT_CA_FILE": filepath.Join(certDir, "ca.crt"),
	})
	echoAddr := waitForEchoAddress(t, echo)

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

	client := &http.Client{Timeout: 30 * time.Second}
	waitUntilReady(t, client, "http://"+iamHTTP+"/v1/health/ready", iam.output)
	waitUntilReady(t, client, "http://"+relayAddress+"/v1/health/ready", relay.output)

	gatewayPort := freeTCPPort(t)
	configPath := writeEnvoyConfigFromTemplate(
		t,
		filepath.Join(repoRoot(t), "infra", "envoy", "envoy.mtls.yaml"),
		gatewayPort,
		iamHTTP,
		iamGRPC,
		echoAddr,
		certDir,
	)
	envoy := startEnvoyContainer(t, configPath, gatewayPort, certDir)
	gatewayBase := fmt.Sprintf("http://127.0.0.1:%d", gatewayPort)
	waitUntilReady(t, client, gatewayBase+"/v1/health/ready", envoy.output)

	// Health bypass remains available over plaintext HTTP while authz/upstream
	// clusters use mTLS. Unauthorized protected calls fail closed.
	live := getGateway(t, client, gatewayBase+"/v1/health/live", nil)
	require.Equal(t, http.StatusOK, live.status)
	unauth := getGateway(t, client, gatewayBase+"/echo", nil)
	if unauth.status != http.StatusUnauthorized && unauth.status != http.StatusForbidden {
		t.Fatalf(
			"expected 401/403, got status=%d body=%s\niam logs:\n%s\nenvoy logs:\n%s",
			unauth.status,
			unauth.body,
			iam.output.String(),
			envoy.output.String(),
		)
	}

	consumerClient, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(brokers, ",")...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeStartOffset(kgo.NewOffset().AtStart()),
	)
	require.NoError(t, err)
	t.Cleanup(consumerClient.Close)

	// Mint a session against IAM HTTP directly; authorize /echo through mTLS Envoy.
	iamBase := "http://" + iamHTTP
	email := "envoy.mtls@example.com"
	password := "correct horse battery staple"
	require.Equal(t, http.StatusAccepted, postJSON(t, client, iamBase+"/v1/registrations", map[string]string{
		"email":    email,
		"password": password,
	}).status)
	challenge := consumeChallenge(t, consumerClient, privateKey)
	require.Equal(t, http.StatusNoContent, postJSON(t, client, iamBase+"/v1/email-verifications", map[string]string{
		"challenge": challenge.token,
	}).status)
	login := postLogin(t, client, iamBase+"/v1/login", map[string]string{
		"email":    email,
		"password": password,
	})
	require.Equal(t, http.StatusNoContent, login.status)

	authed := getGateway(t, client, gatewayBase+"/echo", map[string]string{
		"Cookie": httpapi.AccessTokenCookieName + "=" + login.accessToken,
	})
	require.Equal(t, http.StatusOK, authed.status, authed.body)
	var echoBody struct {
		Headers map[string]string `json:"headers"`
	}
	require.NoError(t, json.Unmarshal([]byte(authed.body), &echoBody), authed.body)
	assert.Equal(t, identity.SubjectKindUser, echoBody.Headers[identity.HeaderSubjectKind])
	assert.Equal(t, identity.AccessLevelStandard, echoBody.Headers[identity.HeaderAccessLevel])
	assert.NotEmpty(t, echoBody.Headers[identity.HeaderUserID])

	iam.stop(t)
	relay.stop(t)
	echo.stop(t)
	envoy.stop(t)
}

func buildProtectedEcho(t *testing.T) string {
	t.Helper()
	binaryPath := filepath.Join(t.TempDir(), "protected-echo")
	command := exec.Command("go", "build", "-o", binaryPath, "../../cmd/protected-echo")
	output, err := command.CombinedOutput()
	require.NoErrorf(t, err, "build protected echo: %s", output)
	return binaryPath
}

func waitForEchoAddress(t *testing.T, process *processHandle) string {
	t.Helper()
	var address string
	require.Eventually(t, func() bool {
		for line := range strings.Lines(process.output.String()) {
			var event struct {
				Address string `json:"addr"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue
			}
			if event.Message == "protected echo listening" && event.Address != "" {
				address = event.Address
				return true
			}
		}
		return false
	}, 15*time.Second, 50*time.Millisecond, process.output.String())
	return address
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func writeEnvoyConfigFromTemplate(
	t *testing.T,
	templatePath string,
	listenPort int,
	iamHTTP string,
	iamGRPC string,
	echoHTTP string,
	certDir string,
) string {
	t.Helper()
	raw, err := os.ReadFile(templatePath)
	require.NoError(t, err)

	_, iamHTTPPort := splitHostPort(t, iamHTTP)
	_, iamGRPCPort := splitHostPort(t, iamGRPC)
	_, echoPort := splitHostPort(t, echoHTTP)

	content := string(raw)
	content = strings.ReplaceAll(content, "__LISTEN_PORT__", strconv.Itoa(listenPort))
	content = strings.ReplaceAll(content, "__IAM_HTTP_HOST__", "host.docker.internal")
	content = strings.ReplaceAll(content, "__IAM_HTTP_PORT__", iamHTTPPort)
	content = strings.ReplaceAll(content, "__IAM_GRPC_HOST__", "host.docker.internal")
	content = strings.ReplaceAll(content, "__IAM_GRPC_PORT__", iamGRPCPort)
	content = strings.ReplaceAll(content, "__ECHO_HTTP_HOST__", "host.docker.internal")
	content = strings.ReplaceAll(content, "__ECHO_HTTP_PORT__", echoPort)
	if certDir != "" {
		content = strings.ReplaceAll(content, "__CA_CERT__", "/certs/ca.crt")
		content = strings.ReplaceAll(content, "__ENVOY_CERT__", "/certs/envoy.crt")
		content = strings.ReplaceAll(content, "__ENVOY_KEY__", "/certs/envoy.key")
	}

	path := filepath.Join(t.TempDir(), "envoy.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func assertEnvoyConfigHasAuthzDeadline(t *testing.T, configPath string) {
	t.Helper()
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	content := string(raw)
	assert.Contains(t, content, "timeout: 0.250s")
	assert.Contains(t, content, "failure_mode_allow: false")
	assert.NotContains(t, content, "retry_policy:")
	assert.Contains(t, content, "remote_address:")
}

func startEnvoyContainer(
	t *testing.T,
	configPath string,
	listenPort int,
	certDir string,
) *processHandle {
	t.Helper()
	output := &synchronizedBuffer{}
	args := []string{
		"run", "--rm",
		"--add-host=host.docker.internal:host-gateway",
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", listenPort, listenPort),
		"-v", configPath + ":/etc/envoy/envoy.yaml:ro",
	}
	if certDir != "" {
		args = append(args, "-v", certDir+":/certs:ro")
	}
	args = append(args,
		"envoyproxy/envoy:v1.34.1",
		"-c", "/etc/envoy/envoy.yaml",
		"--log-level", "info",
	)

	command := exec.Command("docker", args...)
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

func generateTestCerts(t *testing.T) string {
	t.Helper()
	certDir := filepath.Join(t.TempDir(), "certs")
	require.NoError(t, os.MkdirAll(certDir, 0o755))
	script := `
set -euo pipefail
CERT_DIR="$1"
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "$CERT_DIR/ca.key" -out "$CERT_DIR/ca.crt" -days 1 -subj "/CN=test-ca"
for name in iam echo envoy; do
  openssl req -newkey rsa:2048 -nodes \
    -keyout "$CERT_DIR/$name.key" -out "$CERT_DIR/$name.csr" -subj "/CN=$name"
  openssl x509 -req -in "$CERT_DIR/$name.csr" \
    -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" -CAcreateserial \
    -out "$CERT_DIR/$name.crt" -days 1 \
    -extfile <(printf 'subjectAltName=DNS:%s,DNS:localhost,DNS:host.docker.internal,IP:127.0.0.1' "$name")
done
rm -f "$CERT_DIR"/*.csr "$CERT_DIR"/ca.srl
# Envoy runs as a non-root user inside the container and must read these files.
chmod 644 "$CERT_DIR"/*.crt "$CERT_DIR"/*.key
`
	command := exec.Command("bash", "-c", script, "generate-certs", certDir)
	output, err := command.CombinedOutput()
	require.NoErrorf(t, err, "generate certs: %s", output)
	return certDir
}

type gatewayResponse struct {
	jsonResponse
	responseHeaders http.Header
}

func getGateway(
	t *testing.T,
	client *http.Client,
	endpoint string,
	headers map[string]string,
) gatewayResponse {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, endpoint, http.NoBody)
	require.NoError(t, err)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return gatewayResponse{
		jsonResponse: jsonResponse{
			status:      response.StatusCode,
			contentType: response.Header.Get("Content-Type"),
			body:        strings.TrimSpace(string(raw)),
		},
		responseHeaders: response.Header.Clone(),
	}
}

func splitHostPort(t *testing.T, address string) (string, string) {
	t.Helper()
	host, port, err := net.SplitHostPort(address)
	require.NoError(t, err)
	return host, port
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Clean(filepath.Join(wd, "..", "..", "..", ".."))
}
