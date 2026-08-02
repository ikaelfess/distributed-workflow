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

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/httpapi"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestRecoverAndChangePasswords(t *testing.T) {
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
		"PASSWORD_RESET_CHALLENGE_TTL":           "30m",
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

	password := "correct horse battery staple"
	newPassword := "replacement passphrase!"
	email := "reset.user@example.com"

	require.Equal(t, http.StatusAccepted, postJSON(t, client, baseURL+"/v1/registrations", map[string]string{
		"email":    email,
		"password": password,
	}).status)
	verifyChallenge := consumeChallenge(t, consumerClient, privateKey)
	require.Equal(t, http.StatusNoContent, postJSON(t, client, baseURL+"/v1/email-verifications", map[string]string{
		"challenge": verifyChallenge.token,
	}).status)

	// Enumeration resistance: unknown and known emails share the same response.
	unknown := postJSON(t, client, baseURL+"/v1/password-resets", map[string]string{
		"email": "missing.reset@example.com",
	})
	known := postJSON(t, client, baseURL+"/v1/password-resets", map[string]string{
		"email": email,
	})
	require.Equal(t, http.StatusAccepted, unknown.status)
	require.Equal(t, http.StatusAccepted, known.status)
	assert.Equal(t, unknown.body, known.body)
	assert.Equal(t, `{"status":"accepted"}`, known.body)

	resetChallenge := consumeDeliveryChallenge(
		t,
		consumerClient,
		privateKey,
		identity.EmailTemplatePasswordReset,
	)
	assert.NotContains(t, iam.output.String(), resetChallenge.token)
	assert.NotContains(t, iam.output.String(), password)

	loginBefore := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    email,
		"password": password,
	})
	require.Equal(t, http.StatusNoContent, loginBefore.status)
	require.NotEmpty(t, loginBefore.accessToken)

	// Supersession: a second request invalidates the first challenge.
	require.Equal(t, http.StatusAccepted, postJSON(t, client, baseURL+"/v1/password-resets", map[string]string{
		"email": email,
	}).status)
	superseding := consumeDeliveryChallenge(
		t,
		consumerClient,
		privateKey,
		identity.EmailTemplatePasswordReset,
	)
	superseded := postJSON(t, client, baseURL+"/v1/password-reset-completions", map[string]string{
		"challenge":    resetChallenge.token,
		"new_password": newPassword,
	})
	assert.Equal(t, http.StatusBadRequest, superseded.status)

	completed := postJSON(t, client, baseURL+"/v1/password-reset-completions", map[string]string{
		"challenge":    superseding.token,
		"new_password": newPassword,
	})
	require.Equal(t, http.StatusNoContent, completed.status)

	hash := passwordHashFor(t, database, email)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$"))

	// Single use + no auto-login; previous session is dead.
	reuse := postJSON(t, client, baseURL+"/v1/password-reset-completions", map[string]string{
		"challenge":    superseding.token,
		"new_password": "another passphrase!!",
	})
	assert.Equal(t, http.StatusBadRequest, reuse.status)

	staleSessions := doCookieRequest(t, client, http.MethodGet, baseURL+"/v1/authentication-sessions", browserRequest{
		accessToken: loginBefore.accessToken,
	})
	assert.Equal(t, http.StatusUnauthorized, staleSessions.status)

	oldPasswordLogin := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    email,
		"password": password,
	})
	assertAuthFailure(t, oldPasswordLogin)

	freshLogin := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    email,
		"password": newPassword,
	})
	require.Equal(t, http.StatusNoContent, freshLogin.status)

	// Authenticated password change revokes every session including current.
	changed := postBrowserJSON(
		t,
		client,
		baseURL+"/v1/password-changes",
		map[string]string{
			"current_password": newPassword,
			"new_password":     "changed again passphrase",
		},
		browserRequest{
			origin:      "https://app.example.com",
			csrfToken:   freshLogin.csrfToken,
			accessToken: freshLogin.accessToken,
		},
	)
	require.Equal(t, http.StatusNoContent, changed.status)
	assertClearedCredentialCookies(t, changed.cookies)

	afterChange := doCookieRequest(t, client, http.MethodGet, baseURL+"/v1/authentication-sessions", browserRequest{
		accessToken: freshLogin.accessToken,
	})
	assert.Equal(t, http.StatusUnauthorized, afterChange.status)

	require.Equal(t, http.StatusNoContent, postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    email,
		"password": "changed again passphrase",
	}).status)

	// Unverified recovery issues verification, not password-reset.
	unverifiedEmail := "unverified.reset@example.com"
	require.Equal(t, http.StatusAccepted, postJSON(t, client, baseURL+"/v1/registrations", map[string]string{
		"email":    unverifiedEmail,
		"password": "unverified password!",
	}).status)
	_ = consumeChallenge(t, consumerClient, privateKey) // registration verification
	require.Equal(t, http.StatusAccepted, postJSON(t, client, baseURL+"/v1/password-resets", map[string]string{
		"email": unverifiedEmail,
	}).status)
	recoveryVerify := consumeDeliveryChallenge(
		t,
		consumerClient,
		privateKey,
		identity.EmailTemplateVerify,
	)
	require.Equal(t, http.StatusNoContent, postJSON(t, client, baseURL+"/v1/email-verifications", map[string]string{
		"challenge": recoveryVerify.token,
	}).status)
	assertUserState(t, database, unverifiedEmail, "active", "standard", true)

	// Suspended reset keeps the User suspended and never auto-authenticates.
	suspendedEmail := "suspended.reset@example.com"
	require.Equal(t, http.StatusAccepted, postJSON(t, client, baseURL+"/v1/registrations", map[string]string{
		"email":    suspendedEmail,
		"password": password,
	}).status)
	suspendedVerify := consumeChallenge(t, consumerClient, privateKey)
	require.Equal(t, http.StatusNoContent, postJSON(t, client, baseURL+"/v1/email-verifications", map[string]string{
		"challenge": suspendedVerify.token,
	}).status)
	suspendUser(t, database, suspendedEmail)
	require.Equal(t, http.StatusAccepted, postJSON(t, client, baseURL+"/v1/password-resets", map[string]string{
		"email": suspendedEmail,
	}).status)
	suspendedReset := consumeDeliveryChallenge(
		t,
		consumerClient,
		privateKey,
		identity.EmailTemplatePasswordReset,
	)
	require.Equal(t, http.StatusNoContent, postJSON(t, client, baseURL+"/v1/password-reset-completions", map[string]string{
		"challenge":    suspendedReset.token,
		"new_password": "suspended new password",
	}).status)
	assertUserState(t, database, suspendedEmail, "suspended", "standard", true)
	assertAuthFailure(t, postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    suspendedEmail,
		"password": "suspended new password",
	}))

	// Expiry.
	require.Equal(t, http.StatusAccepted, postJSON(t, client, baseURL+"/v1/password-resets", map[string]string{
		"email": email,
	}).status)
	expiring := consumeDeliveryChallenge(
		t,
		consumerClient,
		privateKey,
		identity.EmailTemplatePasswordReset,
	)
	expireActiveChallenges(t, database, email)
	expired := postJSON(t, client, baseURL+"/v1/password-reset-completions", map[string]string{
		"challenge":    expiring.token,
		"new_password": "expired challenge pw",
	})
	assert.Equal(t, http.StatusBadRequest, expired.status)

	// Password policy boundaries match registration.
	short := postJSON(t, client, baseURL+"/v1/password-reset-completions", map[string]string{
		"challenge":    "not-a-real-challenge-token-value!!!!!",
		"new_password": "too-short",
	})
	assert.Equal(t, http.StatusBadRequest, short.status)

	assert.NotContains(t, iam.output.String(), newPassword)
	assert.NotContains(t, iam.output.String(), "changed again passphrase")

	iam.stop(t)
	relay.stop(t)
}

func postBrowserJSON(
	t *testing.T,
	client *http.Client,
	endpoint string,
	body map[string]string,
	request browserRequest,
) cookieResponse {
	t.Helper()
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	httpRequest, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	require.NoError(t, err)
	httpRequest.Header.Set("Content-Type", "application/json")
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
