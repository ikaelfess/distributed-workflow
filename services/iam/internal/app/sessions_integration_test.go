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
	"sync"
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

const testBrowserOrigin = "https://app.example.com"

func TestRotateTokensAndManageAuthenticationSessions(t *testing.T) {
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
		"ALLOWED_ORIGINS":                        testBrowserOrigin,
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

	email := "sessions.user@example.com"
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

	firstLogin := postLoginWithUserAgent(
		t,
		client,
		baseURL+"/v1/login",
		map[string]string{
			"email":    email,
			"password": password,
		},
		"SessionsIntegration/1.0",
	)
	require.Equal(t, http.StatusNoContent, firstLogin.status)
	require.NotEmpty(t, firstLogin.csrfToken)
	assertCookie(t, firstLogin.cookies[httpapi.AccessTokenCookieName])
	assertCookie(t, firstLogin.cookies[httpapi.RefreshTokenCookieName])
	assertCSRFCookie(t, firstLogin.cookies[httpapi.CSRFTokenCookieName])

	corsDenied := doCookieRequest(t, client, http.MethodPost, baseURL+"/v1/refresh", browserRequest{
		origin:       "https://evil.example.com",
		csrfToken:    firstLogin.csrfToken,
		accessToken:  firstLogin.accessToken,
		refreshToken: firstLogin.refreshToken,
	})
	assert.Equal(t, http.StatusForbidden, corsDenied.status)
	assert.Contains(t, corsDenied.body, `"title":"origin forbidden"`)
	assert.Empty(t, corsDenied.accessToken)
	assert.Empty(t, corsDenied.refreshToken)
	assert.NotContains(t, corsDenied.cookies, httpapi.AccessTokenCookieName)
	assert.NotContains(t, corsDenied.cookies, httpapi.RefreshTokenCookieName)

	wrongCSRF, _, err := identity.NewOpaqueToken(nil)
	require.NoError(t, err)
	csrfDenied := doCookieRequest(t, client, http.MethodPost, baseURL+"/v1/refresh", browserRequest{
		origin:       testBrowserOrigin,
		csrfToken:    wrongCSRF,
		accessToken:  firstLogin.accessToken,
		refreshToken: firstLogin.refreshToken,
	})
	assert.Equal(t, http.StatusForbidden, csrfDenied.status)
	assert.Contains(t, csrfDenied.body, `"title":"csrf failed"`)
	assert.Empty(t, csrfDenied.accessToken)
	assert.Empty(t, csrfDenied.refreshToken)

	refreshed := doCookieRequest(t, client, http.MethodPost, baseURL+"/v1/refresh", browserRequest{
		origin:       testBrowserOrigin,
		csrfToken:    firstLogin.csrfToken,
		accessToken:  firstLogin.accessToken,
		refreshToken: firstLogin.refreshToken,
	})
	require.Equal(t, http.StatusNoContent, refreshed.status)
	assert.NotEqual(t, firstLogin.accessToken, refreshed.accessToken)
	assert.NotEqual(t, firstLogin.refreshToken, refreshed.refreshToken)
	assertCookie(t, refreshed.cookies[httpapi.AccessTokenCookieName])
	assertCookie(t, refreshed.cookies[httpapi.RefreshTokenCookieName])
	assert.NotContains(t, iam.output.String(), refreshed.accessToken)
	assert.NotContains(t, iam.output.String(), refreshed.refreshToken)

	conn, err := grpc.NewClient(
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	validator := iamv1.NewTokenValidationServiceClient(conn)

	_, err = validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: firstLogin.accessToken,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	valid, err := validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: refreshed.accessToken,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, valid.GetAuthenticationSessionId())

	concurrentLogin := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    email,
		"password": password,
	})
	require.Equal(t, http.StatusNoContent, concurrentLogin.status)

	var (
		wg           sync.WaitGroup
		firstResult  cookieResponse
		secondResult cookieResponse
		firstOnce    sync.Once
		secondOnce   sync.Once
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		result := doCookieRequest(t, client, http.MethodPost, baseURL+"/v1/refresh", browserRequest{
			origin:       testBrowserOrigin,
			csrfToken:    concurrentLogin.csrfToken,
			accessToken:  concurrentLogin.accessToken,
			refreshToken: concurrentLogin.refreshToken,
		})
		firstOnce.Do(func() { firstResult = result })
	}()
	go func() {
		defer wg.Done()
		result := doCookieRequest(t, client, http.MethodPost, baseURL+"/v1/refresh", browserRequest{
			origin:       testBrowserOrigin,
			csrfToken:    concurrentLogin.csrfToken,
			accessToken:  concurrentLogin.accessToken,
			refreshToken: concurrentLogin.refreshToken,
		})
		secondOnce.Do(func() { secondResult = result })
	}()
	wg.Wait()

	successes := 0
	failures := 0
	var winner cookieResponse
	for _, result := range []cookieResponse{firstResult, secondResult} {
		switch result.status {
		case http.StatusNoContent:
			successes++
			winner = result
		case http.StatusUnauthorized:
			failures++
			assert.Empty(t, result.accessToken)
			assert.Empty(t, result.refreshToken)
		default:
			t.Fatalf("unexpected concurrent refresh status %d: %s", result.status, result.body)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, failures)
	require.NotEmpty(t, winner.accessToken)
	for _, result := range []cookieResponse{firstResult, secondResult} {
		if result.status != http.StatusUnauthorized {
			continue
		}
		assert.NotContains(t, result.cookies, httpapi.AccessTokenCookieName)
		assert.NotContains(t, result.cookies, httpapi.RefreshTokenCookieName)
		assert.NotContains(t, result.cookies, httpapi.CSRFTokenCookieName)
	}
	_, err = validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: winner.accessToken,
	})
	require.NoError(t, err)
	assert.Equal(
		t,
		int64(1),
		countActiveSessionsForRefreshToken(t, database, concurrentLogin.refreshToken),
		"concurrent refresh within grace must not revoke the token family",
	)
	assert.GreaterOrEqual(
		t,
		countAuditEvents(t, database, "authentication.refresh.rejected"),
		int64(1),
	)

	reuseLogin := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    email,
		"password": password,
	})
	require.Equal(t, http.StatusNoContent, reuseLogin.status)
	rotated := doCookieRequest(t, client, http.MethodPost, baseURL+"/v1/refresh", browserRequest{
		origin:       testBrowserOrigin,
		csrfToken:    reuseLogin.csrfToken,
		accessToken:  reuseLogin.accessToken,
		refreshToken: reuseLogin.refreshToken,
	})
	require.Equal(t, http.StatusNoContent, rotated.status)
	backdateRefreshSuccessor(t, database, reuseLogin.refreshToken, 10*time.Second)
	reuse := doCookieRequest(t, client, http.MethodPost, baseURL+"/v1/refresh", browserRequest{
		origin:       testBrowserOrigin,
		csrfToken:    reuseLogin.csrfToken,
		accessToken:  rotated.accessToken,
		refreshToken: reuseLogin.refreshToken,
	})
	require.Equal(t, http.StatusUnauthorized, reuse.status)
	assert.Empty(t, reuse.accessToken)
	assert.Empty(t, reuse.refreshToken)
	_, err = validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: rotated.accessToken,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	assert.GreaterOrEqual(
		t,
		countAuditEvents(t, database, "authentication.refresh.reuse_detected"),
		int64(1),
	)

	sessionA := postLoginWithUserAgent(
		t,
		client,
		baseURL+"/v1/login",
		map[string]string{
			"email":    email,
			"password": password,
		},
		"SessionsIntegration/device-a",
	)
	require.Equal(t, http.StatusNoContent, sessionA.status)
	sessionB := postLoginWithUserAgent(
		t,
		client,
		baseURL+"/v1/login",
		map[string]string{
			"email":    email,
			"password": password,
		},
		"SessionsIntegration/device-b",
	)
	require.Equal(t, http.StatusNoContent, sessionB.status)
	identityA, err := validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: sessionA.accessToken,
	})
	require.NoError(t, err)
	identityB, err := validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: sessionB.accessToken,
	})
	require.NoError(t, err)
	require.NotEqual(t, identityA.GetAuthenticationSessionId(), identityB.GetAuthenticationSessionId())

	listed := doCookieRequest(t, client, http.MethodGet, baseURL+"/v1/authentication-sessions", browserRequest{
		accessToken: sessionB.accessToken,
	})
	require.Equal(t, http.StatusOK, listed.status)
	var listBody struct {
		AuthenticationSessions []struct {
			ID             string `json:"id"`
			CreatedAt      string `json:"created_at"`
			LastUsedAt     string `json:"last_used_at"`
			ClientMetadata string `json:"client_metadata"`
			IP             string `json:"ip"`
			Current        bool   `json:"current"`
		} `json:"authentication_sessions"`
	}
	require.NoError(t, json.Unmarshal([]byte(listed.body), &listBody))
	require.GreaterOrEqual(t, len(listBody.AuthenticationSessions), 2)
	assert.NotContains(t, listed.body, sessionB.accessToken)
	assert.NotContains(t, listed.body, sessionB.refreshToken)
	assert.NotContains(t, listed.body, sessionB.csrfToken)
	currentCount := 0
	foundA := false
	for _, session := range listBody.AuthenticationSessions {
		assert.NotEmpty(t, session.ID)
		assert.NotEmpty(t, session.CreatedAt)
		assert.NotEmpty(t, session.LastUsedAt)
		assert.NotEmpty(t, session.ClientMetadata)
		if session.Current {
			currentCount++
			assert.Equal(t, identityB.GetAuthenticationSessionId(), session.ID)
			assert.Equal(t, "SessionsIntegration/device-b", session.ClientMetadata)
			assert.NotEmpty(t, session.IP)
		}
		if session.ID == identityA.GetAuthenticationSessionId() {
			foundA = true
			assert.False(t, session.Current)
			assert.Equal(t, "SessionsIntegration/device-a", session.ClientMetadata)
		}
	}
	assert.Equal(t, 1, currentCount)
	require.True(t, foundA)

	logoutCSRFDenied := doCookieRequest(t, client, http.MethodPost, baseURL+"/v1/logout", browserRequest{
		origin:      testBrowserOrigin,
		csrfToken:   wrongCSRF,
		accessToken: sessionB.accessToken,
	})
	assert.Equal(t, http.StatusForbidden, logoutCSRFDenied.status)
	assert.Contains(t, logoutCSRFDenied.body, `"title":"csrf failed"`)

	revokeOther := doCookieRequest(
		t,
		client,
		http.MethodDelete,
		baseURL+"/v1/authentication-sessions/"+identityA.GetAuthenticationSessionId(),
		browserRequest{
			origin:      testBrowserOrigin,
			csrfToken:   sessionB.csrfToken,
			accessToken: sessionB.accessToken,
		},
	)
	require.Equal(t, http.StatusNoContent, revokeOther.status)
	assert.Empty(t, revokeOther.accessToken)
	_, err = validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: sessionA.accessToken,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: sessionB.accessToken,
	})
	require.NoError(t, err)

	logout := doCookieRequest(t, client, http.MethodPost, baseURL+"/v1/logout", browserRequest{
		origin:      testBrowserOrigin,
		csrfToken:   sessionB.csrfToken,
		accessToken: sessionB.accessToken,
	})
	require.Equal(t, http.StatusNoContent, logout.status)
	assertClearedCredentialCookies(t, logout.cookies)
	_, err = validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: sessionB.accessToken,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	sessionC := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    email,
		"password": password,
	})
	require.Equal(t, http.StatusNoContent, sessionC.status)
	sessionD := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    email,
		"password": password,
	})
	require.Equal(t, http.StatusNoContent, sessionD.status)
	revokeAll := doCookieRequest(
		t,
		client,
		http.MethodDelete,
		baseURL+"/v1/authentication-sessions",
		browserRequest{
			origin:      testBrowserOrigin,
			csrfToken:   sessionD.csrfToken,
			accessToken: sessionD.accessToken,
		},
	)
	require.Equal(t, http.StatusNoContent, revokeAll.status)
	assertClearedCredentialCookies(t, revokeAll.cookies)
	_, err = validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: sessionC.accessToken,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
	_, err = validator.ValidateToken(t.Context(), &iamv1.ValidateTokenRequest{
		AccessToken: sessionD.accessToken,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	assert.GreaterOrEqual(t, countAuditEvents(t, database, "authentication.refresh.succeeded"), int64(2))
	assert.GreaterOrEqual(t, countAuditEvents(t, database, "authentication.logout.succeeded"), int64(1))
	assert.GreaterOrEqual(t, countAuditEvents(t, database, "authentication.session.revoked"), int64(1))
	assert.GreaterOrEqual(t, countAuditEvents(t, database, "authentication.sessions.revoked_all"), int64(1))
	assertSessionAuditMetadataIsSafe(
		t,
		database,
		password,
		sessionD.accessToken,
		sessionD.refreshToken,
		sessionD.csrfToken,
	)

	iam.stop(t)
	relay.stop(t)
}

type browserRequest struct {
	origin       string
	csrfToken    string
	accessToken  string
	refreshToken string
}

func postLoginWithUserAgent(
	t *testing.T,
	client *http.Client,
	endpoint string,
	body map[string]string,
	userAgent string,
) cookieResponse {
	t.Helper()
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	httpRequest, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	require.NoError(t, err)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", userAgent)
	response, err := client.Do(httpRequest)
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

func doCookieRequest(
	t *testing.T,
	client *http.Client,
	method string,
	endpoint string,
	request browserRequest,
) cookieResponse {
	t.Helper()
	httpRequest, err := http.NewRequest(method, endpoint, http.NoBody)
	require.NoError(t, err)
	if request.origin != "" {
		httpRequest.Header.Set("Origin", request.origin)
	}
	if request.csrfToken != "" {
		httpRequest.Header.Set(httpapi.CSRFTokenHeaderName, request.csrfToken)
	}
	if request.accessToken != "" {
		httpRequest.AddCookie(&http.Cookie{
			Name:  httpapi.AccessTokenCookieName,
			Value: request.accessToken,
		})
	}
	if request.refreshToken != "" {
		httpRequest.AddCookie(&http.Cookie{
			Name:  httpapi.RefreshTokenCookieName,
			Value: request.refreshToken,
		})
	}
	if request.csrfToken != "" {
		httpRequest.AddCookie(&http.Cookie{
			Name:  httpapi.CSRFTokenCookieName,
			Value: request.csrfToken,
		})
	}

	response, err := client.Do(httpRequest)
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

func assertCSRFCookie(t *testing.T, cookie *http.Cookie) {
	t.Helper()
	require.NotNil(t, cookie)
	assert.NotEmpty(t, cookie.Value)
	assert.False(t, cookie.HttpOnly)
	assert.True(t, cookie.Secure)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	assert.Equal(t, "/", cookie.Path)
	assert.Empty(t, cookie.Domain)
}

func assertClearedCredentialCookies(t *testing.T, cookies map[string]*http.Cookie) {
	t.Helper()
	for _, name := range []string{
		httpapi.AccessTokenCookieName,
		httpapi.RefreshTokenCookieName,
		httpapi.CSRFTokenCookieName,
	} {
		cookie := cookies[name]
		require.NotNil(t, cookie, name)
		assert.Equal(t, -1, cookie.MaxAge, name)
		assert.Empty(t, cookie.Value, name)
	}
}

func backdateRefreshSuccessor(
	t *testing.T,
	database *postgres.Database,
	oldRefreshToken string,
	age time.Duration,
) {
	t.Helper()
	tokenHash, err := identity.HashOpaqueToken(oldRefreshToken)
	require.NoError(t, err)
	tag, err := database.Exec(t.Context(), `
		UPDATE refresh_tokens
		SET created_at = NOW() - ($2 * INTERVAL '1 millisecond')
		WHERE rotated_from_id = (
			SELECT id FROM refresh_tokens WHERE token_hash = $1
		)
	`, tokenHash, age.Milliseconds())
	require.NoError(t, err)
	require.Equal(t, int64(1), tag.RowsAffected())
}

func countActiveSessionsForRefreshToken(
	t *testing.T,
	database *postgres.Database,
	refreshToken string,
) int64 {
	t.Helper()
	tokenHash, err := identity.HashOpaqueToken(refreshToken)
	require.NoError(t, err)
	rows, err := database.Query(t.Context(), `
		SELECT COUNT(*)
		FROM authentication_sessions s
		WHERE s.id = (
			SELECT session_id FROM refresh_tokens WHERE token_hash = $1
		)
		  AND s.revoked_at IS NULL
	`, tokenHash)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var count int64
	require.NoError(t, rows.Scan(&count))
	return count
}

func assertSessionAuditMetadataIsSafe(
	t *testing.T,
	database *postgres.Database,
	password string,
	accessToken string,
	refreshToken string,
	csrfToken string,
) {
	t.Helper()
	rows, err := database.Query(t.Context(), `
		SELECT event_type, metadata::text
		FROM audit_events
		WHERE event_type LIKE 'authentication.%'
	`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var eventType, metadata string
		require.NoError(t, rows.Scan(&eventType, &metadata))
		assert.NotContains(t, metadata, password)
		assert.NotContains(t, metadata, accessToken)
		assert.NotContains(t, metadata, refreshToken)
		assert.NotContains(t, metadata, csrfToken)
		assert.NotContains(t, metadata, "sessions.user@example.com")
		_ = eventType
	}
	require.NoError(t, rows.Err())
}
