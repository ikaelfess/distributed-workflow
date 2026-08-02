package grpcapi

import (
	"context"
	"errors"
	"strings"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	authv3 "github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/httpapi"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
)

func (s *Server) Check(
	ctx context.Context,
	request *authv3.CheckRequest,
) (*authv3.CheckResponse, error) {
	rawToken := accessTokenFromCheckRequest(request)
	if rawToken == "" {
		return deniedCheckResponse(typev3.StatusCode_Unauthorized, "authentication required"), nil
	}

	identityResult, err := s.validate.ValidateAccessToken(ctx, rawToken)
	if errors.Is(err, identity.ErrMalformedAccessToken) ||
		errors.Is(err, identity.ErrInvalidAccessToken) {
		return deniedCheckResponse(typev3.StatusCode_Unauthorized, "authentication required"), nil
	}
	if err != nil {
		s.logger.Error().Err(err).Msg("authorization check failed")
		return deniedCheckResponse(
			typev3.StatusCode_ServiceUnavailable,
			"authorization temporarily unavailable",
		), nil
	}

	headers := []*corev3.HeaderValueOption{
		identityHeader(identity.HeaderUserID, identityResult.UserID),
		identityHeader(identity.HeaderSubjectKind, identityResult.SubjectKind),
		identityHeader(identity.HeaderAccessLevel, identityResult.AccessLevel),
		identityHeader(
			identity.HeaderAuthenticationSessionID,
			identityResult.AuthenticationSessionID,
		),
		identityHeader(
			identity.HeaderAccessTokenExpiresAt,
			identityResult.ExpiresAt.UTC().Format(time.RFC3339Nano),
		),
	}
	return &authv3.CheckResponse{
		Status: &status.Status{Code: int32(codes.OK)},
		HttpResponse: &authv3.CheckResponse_OkResponse{
			OkResponse: &authv3.OkHttpResponse{
				// Default append=false overwrites any caller-supplied values for
				// these trusted header names. Avoid headers_to_remove here: some
				// Envoy builds remove and then fail to re-add the same keys.
				Headers: headers,
			},
		},
	}, nil
}

func accessTokenFromCheckRequest(request *authv3.CheckRequest) string {
	if request == nil || request.GetAttributes() == nil ||
		request.GetAttributes().GetRequest() == nil ||
		request.GetAttributes().GetRequest().GetHttp() == nil {
		return ""
	}
	headers := request.GetAttributes().GetRequest().GetHttp().GetHeaders()
	if len(headers) == 0 {
		return ""
	}
	// Optional explicit extraction header configured in Envoy; never log its value.
	if token := strings.TrimSpace(headers["x-"+httpapi.AccessTokenCookieName]); token != "" {
		return token
	}
	return cookieValue(headers["cookie"], httpapi.AccessTokenCookieName)
}

func cookieValue(cookieHeader, name string) string {
	for part := range strings.SplitSeq(cookieHeader, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) != name {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}

func identityHeader(name, value string) *corev3.HeaderValueOption {
	return &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:   name,
			Value: value,
		},
	}
}

func deniedCheckResponse(code typev3.StatusCode, body string) *authv3.CheckResponse {
	rpcCode := codes.Unauthenticated
	if code == typev3.StatusCode_ServiceUnavailable {
		rpcCode = codes.Unavailable
	}
	return &authv3.CheckResponse{
		Status: &status.Status{Code: int32(rpcCode)},
		HttpResponse: &authv3.CheckResponse_DeniedResponse{
			DeniedResponse: &authv3.DeniedHttpResponse{
				Status: &typev3.HttpStatus{Code: code},
				Body:   body,
				Headers: []*corev3.HeaderValueOption{
					{
						Header: &corev3.HeaderValue{
							Key:   "content-type",
							Value: "text/plain; charset=utf-8",
						},
					},
				},
			},
		},
	}
}
