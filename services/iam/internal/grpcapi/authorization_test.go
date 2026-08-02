package grpcapi

import (
	"testing"

	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/httpapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccessTokenFromCheckRequest_CookieAndHeader(t *testing.T) {
	t.Parallel()

	t.Run("reads access token cookie", func(t *testing.T) {
		t.Parallel()
		request := &authv3.CheckRequest{
			Attributes: &authv3.AttributeContext{
				Request: &authv3.AttributeContext_Request{
					Http: &authv3.AttributeContext_HttpRequest{
						Headers: map[string]string{
							"cookie": httpapi.AccessTokenCookieName + "=opaque-token-value; other=1",
						},
					},
				},
			},
		}
		assert.Equal(t, "opaque-token-value", accessTokenFromCheckRequest(request))
	})

	t.Run("prefers explicit extraction header", func(t *testing.T) {
		t.Parallel()
		request := &authv3.CheckRequest{
			Attributes: &authv3.AttributeContext{
				Request: &authv3.AttributeContext_Request{
					Http: &authv3.AttributeContext_HttpRequest{
						Headers: map[string]string{
							"cookie":             "iam_access_token=from-cookie",
							"x-iam_access_token": "from-header",
						},
					},
				},
			},
		}
		assert.Equal(t, "from-header", accessTokenFromCheckRequest(request))
	})

	t.Run("missing cookie returns empty", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, accessTokenFromCheckRequest(&authv3.CheckRequest{}))
	})
}
