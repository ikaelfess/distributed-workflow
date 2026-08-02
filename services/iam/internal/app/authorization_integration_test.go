//go:build integration

package app_test

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/httpapi"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
)

func TestAuthorizationCheckSharesValidateTokenUseCase(t *testing.T) {
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

	email := "check.user@example.com"
	password := "correct horse battery staple"
	require.Equal(t, http.StatusAccepted, postJSON(t, client, baseURL+"/v1/registrations", map[string]string{
		"email":    email,
		"password": password,
	}).status)
	challenge := consumeChallenge(t, consumerClient, privateKey)
	require.Equal(t, http.StatusNoContent, postJSON(t, client, baseURL+"/v1/email-verifications", map[string]string{
		"challenge": challenge.token,
	}).status)

	login := postLogin(t, client, baseURL+"/v1/login", map[string]string{
		"email":    email,
		"password": password,
	})
	require.Equal(t, http.StatusNoContent, login.status)

	conn, err := grpc.NewClient(
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	authz := authv3.NewAuthorizationClient(conn)

	ok, err := authz.Check(t.Context(), checkRequestWithCookie(login.accessToken))
	require.NoError(t, err)
	require.Equal(t, int32(codes.OK), ok.GetStatus().GetCode())
	require.NotNil(t, ok.GetOkResponse())
	headers := map[string]string{}
	for _, header := range ok.GetOkResponse().GetHeaders() {
		headers[strings.ToLower(header.GetHeader().GetKey())] = header.GetHeader().GetValue()
	}
	assert.NotEmpty(t, headers[identity.HeaderUserID])
	assert.Equal(t, identity.SubjectKindUser, headers[identity.HeaderSubjectKind])
	assert.Equal(t, identity.AccessLevelStandard, headers[identity.HeaderAccessLevel])
	assert.NotEmpty(t, headers[identity.HeaderAuthenticationSessionID])
	assert.NotEmpty(t, headers[identity.HeaderAccessTokenExpiresAt])
	gotKeys := make([]string, 0, len(headers))
	for key := range headers {
		gotKeys = append(gotKeys, key)
	}
	assert.ElementsMatch(t, identity.TrustedIdentityHeaders(), gotKeys)
	assert.Empty(t, ok.GetOkResponse().GetHeadersToRemove())
	assert.NotContains(t, iam.output.String(), login.accessToken)

	denied, err := authz.Check(t.Context(), checkRequestWithCookie("not-a-valid-token"))
	require.NoError(t, err)
	assert.Equal(t, int32(codes.Unauthenticated), denied.GetStatus().GetCode())
	assert.Equal(t, typev3.StatusCode_Unauthorized, denied.GetDeniedResponse().GetStatus().GetCode())

	missing, err := authz.Check(t.Context(), &authv3.CheckRequest{})
	require.NoError(t, err)
	assert.Equal(t, int32(codes.Unauthenticated), missing.GetStatus().GetCode())

	iam.stop(t)
	relay.stop(t)
}

func checkRequestWithCookie(accessToken string) *authv3.CheckRequest {
	return &authv3.CheckRequest{
		Attributes: &authv3.AttributeContext{
			Request: &authv3.AttributeContext_Request{
				Http: &authv3.AttributeContext_HttpRequest{
					Headers: map[string]string{
						"cookie": httpapi.AccessTokenCookieName + "=" + accessToken,
					},
				},
			},
		},
	}
}
