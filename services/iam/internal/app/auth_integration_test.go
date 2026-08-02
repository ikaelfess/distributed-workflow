//go:build integration

package app_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	iamv1 "github.com/ikaelfess/distributed-workflow/services/iam/api/gen/iam/v1"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/httpapi"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestAuthenticateAndValidateAccessToken(t *testing.T) {
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
		"ACCESS_TOKEN_TTL":                       "15m",
		"REFRESH_TOKEN_TTL":                      "720h",
		"ALLOWED_ORIGINS":                        "https://app.example.com",
		"REFRESH_REUSE_GRACE_PERIOD":             "5s",
	})
	httpAddress := iam.waitForAddress(t)
	grpcAddress := iam.waitForGRPCAddress(t)
	client := &http.Client{Timeout: 30 * time.Second}
	baseURL := "http://" + httpAddress
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

	email := "Auth.User@Example.COM"
	password := "correct horse battery staple"
	register := postJSON(t, client, baseURL+"/v1/registrations", map[string]string{
		"email":    email,
		"password": password,
	})
	require.Equal(t, http.StatusAccepted, register.status)
	challenge := consumeChallenge(t, consumerClient, privateKey)
	verified := postJSON(t, client, baseURL+"/v1/email-verifications", map[string]string{
		"challenge": challenge.token,
	})
	require.Equal(t, http.StatusNoContent, verified.status)

	unverifiedRegister := postJSON(t, client, baseURL+"/v1/registrations", map[string]string{
		"email":    "unverified.login@example.com",
		"password": "unverified password!",
	})
	require.Equal(t, http.StatusAccepted, unverifiedRegister.status)

	unknown := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    "missing@example.com",
		"password": password,
	})
	assertAuthFailure(t, unknown)

	wrongPassword := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    email,
		"password": "totally wrong password",
	})
	assertAuthFailure(t, wrongPassword)
	assert.Equal(t, unknown.body, wrongPassword.body)

	unverified := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    "unverified.login@example.com",
		"password": "unverified password!",
	})
	assertAuthFailure(t, unverified)
	assert.Equal(t, unknown.body, unverified.body)

	success := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    "auth.user@example.com",
		"password": password,
	})
	require.Equal(t, http.StatusNoContent, success.status)
	assert.Empty(t, success.body)
	assert.NotContains(t, iam.output.String(), password)
	assert.NotContains(t, iam.output.String(), success.accessToken)
	assert.NotContains(t, iam.output.String(), success.refreshToken)
	assertCookie(t, success.cookies[httpapi.AccessTokenCookieName])
	assertCookie(t, success.cookies[httpapi.RefreshTokenCookieName])
	assert.NotEmpty(t, success.accessToken)
	assert.NotEmpty(t, success.refreshToken)
	assert.Equal(t, int64(1), countAuthenticationSessions(t, database))
	assert.Equal(t, int64(1), countAccessTokens(t, database))
	assert.Equal(t, int64(1), countRefreshTokens(t, database))
	assertNoPersistedRawTokens(t, database, success.accessToken, success.refreshToken)

	secondLogin := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    email,
		"password": password,
	})
	require.Equal(t, http.StatusNoContent, secondLogin.status)
	assert.NotEqual(t, success.accessToken, secondLogin.accessToken)
	assert.Equal(t, int64(2), countAuthenticationSessions(t, database))

	conn, err := grpc.NewClient(
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	validator := iamv1.NewTokenValidationServiceClient(conn)

	valid, err := validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: success.accessToken,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, valid.GetUserId())
	assert.Equal(t, iamv1.SubjectKind_SUBJECT_KIND_USER, valid.GetSubjectKind())
	assert.Equal(t, iamv1.AccessLevel_ACCESS_LEVEL_STANDARD, valid.GetAccessLevel())
	assert.NotEmpty(t, valid.GetAuthenticationSessionId())
	assert.True(t, valid.GetExpiresAt().AsTime().After(time.Now().UTC()))
	assert.NotContains(t, valid.String(), "auth.user@example.com")
	assert.NotContains(t, valid.String(), success.accessToken)

	again, err := validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: success.accessToken,
	})
	require.NoError(t, err)
	assert.Equal(t, valid.GetUserId(), again.GetUserId())
	assert.Equal(t, valid.GetAuthenticationSessionId(), again.GetAuthenticationSessionId())

	_, err = validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: "not-a-valid-token",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: "",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	expireAccessToken(t, database, success.accessToken)
	_, err = validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: success.accessToken,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	suspendUser(t, database, "auth.user@example.com")
	_, err = validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: secondLogin.accessToken,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	suspendedLogin := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    email,
		"password": password,
	})
	assertAuthFailure(t, suspendedLogin)
	assert.Equal(t, unknown.body, suspendedLogin.body)

	restoreActiveUser(t, database, "auth.user@example.com")
	thirdLogin := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    email,
		"password": password,
	})
	require.Equal(t, http.StatusNoContent, thirdLogin.status)
	revokeAuthenticationSession(t, database, thirdLogin.accessToken)
	_, err = validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: thirdLogin.accessToken,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	assert.GreaterOrEqual(t, countAuditEvents(t, database, "authentication.succeeded"), int64(3))
	assert.GreaterOrEqual(t, countAuditEvents(t, database, "authentication.failed"), int64(4))
	assert.GreaterOrEqual(t, countAuditEvents(t, database, "validation.succeeded"), int64(2))
	assert.GreaterOrEqual(t, countAuditEvents(t, database, "validation.failed"), int64(4))
	assertAuditMetadataIsSafe(t, database, password, success.accessToken, success.refreshToken)

	iam.stop(t)
	relay.stop(t)
}

type cookieResponse struct {
	status       int
	body         string
	accessToken  string
	refreshToken string
	csrfToken    string
	cookies      map[string]*http.Cookie
}

func postLogin(
	t *testing.T,
	client *http.Client,
	endpoint string,
	body map[string]string,
) cookieResponse {
	t.Helper()
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	response, err := client.Post(endpoint, "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	cookies := map[string]*http.Cookie{}
	for _, cookie := range response.Cookies() {
		cookies[cookie.Name] = cookie
	}
	return cookieResponse{
		status:       response.StatusCode,
		body:         strings.TrimSpace(string(raw)),
		accessToken:  cookieValue(cookies, httpapi.AccessTokenCookieName),
		refreshToken: cookieValue(cookies, httpapi.RefreshTokenCookieName),
		csrfToken:    cookieValue(cookies, httpapi.CSRFTokenCookieName),
		cookies:      cookies,
	}
}

func assertAuthFailure(t *testing.T, response cookieResponse) {
	t.Helper()
	assert.Equal(t, http.StatusUnauthorized, response.status)
	assert.Contains(t, response.body, `"title":"authentication failed"`)
	assert.Empty(t, response.accessToken)
	assert.Empty(t, response.refreshToken)
	assert.Empty(t, response.csrfToken)
}

func assertCookie(t *testing.T, cookie *http.Cookie) {
	t.Helper()
	require.NotNil(t, cookie)
	assert.NotEmpty(t, cookie.Value)
	assert.True(t, cookie.HttpOnly)
	assert.True(t, cookie.Secure)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	assert.Equal(t, "/", cookie.Path)
	assert.Empty(t, cookie.Domain)
}

func cookieValue(cookies map[string]*http.Cookie, name string) string {
	cookie, ok := cookies[name]
	if !ok || cookie == nil {
		return ""
	}
	return cookie.Value
}

func (p *processHandle) waitForGRPCAddress(t *testing.T) string {
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
			if event.Message == "grpc server started" && event.Address != "" {
				address = event.Address
				return true
			}
		}
		return false
	}, 15*time.Second, 50*time.Millisecond, p.output.String())
	return address
}

func countAccessTokens(t *testing.T, database *postgres.Database) int64 {
	t.Helper()
	return countQuery(t, database, `SELECT COUNT(*) FROM access_tokens`)
}

func countRefreshTokens(t *testing.T, database *postgres.Database) int64 {
	t.Helper()
	return countQuery(t, database, `SELECT COUNT(*) FROM refresh_tokens`)
}

func assertNoPersistedRawTokens(
	t *testing.T,
	database *postgres.Database,
	accessToken string,
	refreshToken string,
) {
	t.Helper()
	accessHash, err := identity.HashOpaqueToken(accessToken)
	require.NoError(t, err)
	refreshHash, err := identity.HashOpaqueToken(refreshToken)
	require.NoError(t, err)

	rows, err := database.Query(t.Context(), `
		SELECT COUNT(*) FROM access_tokens WHERE token_hash = $1
	`, accessHash)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var accessCount int64
	require.NoError(t, rows.Scan(&accessCount))
	assert.Equal(t, int64(1), accessCount)

	rows, err = database.Query(t.Context(), `
		SELECT COUNT(*) FROM refresh_tokens WHERE token_hash = $1
	`, refreshHash)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var refreshCount int64
	require.NoError(t, rows.Scan(&refreshCount))
	assert.Equal(t, int64(1), refreshCount)

	assert.NotEqual(t, accessToken, string(accessHash))
	assert.NotEqual(t, refreshToken, string(refreshHash))
}

func countAuditEvents(t *testing.T, database *postgres.Database, eventType string) int64 {
	t.Helper()
	rows, err := database.Query(t.Context(), `
		SELECT COUNT(*) FROM audit_events WHERE event_type = $1
	`, eventType)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var count int64
	require.NoError(t, rows.Scan(&count))
	return count
}

func expireAccessToken(t *testing.T, database *postgres.Database, rawToken string) {
	t.Helper()
	tokenHash, err := identity.HashOpaqueToken(rawToken)
	require.NoError(t, err)
	tag, err := database.Exec(t.Context(), `
		UPDATE access_tokens
		SET expires_at = NOW() - INTERVAL '1 minute'
		WHERE token_hash = $1
	`, tokenHash)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected())
}

func revokeAuthenticationSession(t *testing.T, database *postgres.Database, rawToken string) {
	t.Helper()
	tokenHash, err := identity.HashOpaqueToken(rawToken)
	require.NoError(t, err)
	tag, err := database.Exec(t.Context(), `
		UPDATE authentication_sessions
		SET revoked_at = NOW()
		WHERE id = (
			SELECT session_id FROM access_tokens WHERE token_hash = $1
		)
	`, tokenHash)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected())
}

func suspendUser(t *testing.T, database *postgres.Database, email string) {
	t.Helper()
	tag, err := database.Exec(t.Context(), `
		UPDATE users
		SET status = 'suspended',
		    updated_at = NOW()
		WHERE email = $1
	`, email)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected())
}

func restoreActiveUser(t *testing.T, database *postgres.Database, email string) {
	t.Helper()
	tag, err := database.Exec(t.Context(), `
		UPDATE users
		SET status = 'active',
		    updated_at = NOW()
		WHERE email = $1
		  AND email_verified_at IS NOT NULL
	`, email)
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected())
}

func assertAuditMetadataIsSafe(
	t *testing.T,
	database *postgres.Database,
	password string,
	accessToken string,
	refreshToken string,
) {
	t.Helper()
	rows, err := database.Query(t.Context(), `
		SELECT event_type, metadata::text
		FROM audit_events
		WHERE event_type IN (
			'authentication.succeeded',
			'authentication.failed',
			'validation.succeeded',
			'validation.failed'
		)
	`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var eventType, metadata string
		require.NoError(t, rows.Scan(&eventType, &metadata))
		assert.NotContains(t, metadata, password)
		assert.NotContains(t, metadata, accessToken)
		assert.NotContains(t, metadata, refreshToken)
		assert.NotContains(t, metadata, "auth.user@example.com")
		assert.Contains(t, metadata, `"outcome"`)
		_ = eventType
	}
	require.NoError(t, rows.Err())
}
