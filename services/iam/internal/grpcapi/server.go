package grpcapi

import (
	"context"
	"errors"
	"fmt"
	"net"

	iamv1 "github.com/ikaelfess/distributed-workflow/services/iam/api/gen/iam/v1"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Server struct {
	iamv1.UnimplementedTokenValidationServiceServer
	validate *identity.ValidateService
	server   *grpc.Server
	logger   zerolog.Logger
}

func NewServer(validate *identity.ValidateService, logger zerolog.Logger) *Server {
	grpcServer := grpc.NewServer()
	server := &Server{
		validate: validate,
		server:   grpcServer,
		logger:   logger,
	}
	iamv1.RegisterTokenValidationServiceServer(grpcServer, server)
	return server
}

func (s *Server) Serve(listener net.Listener) error {
	s.logger.Info().Str("addr", listener.Addr().String()).Msg("grpc server started")
	if err := s.server.Serve(listener); err != nil {
		return fmt.Errorf("serve grpc: %w", err)
	}
	return nil
}

func (s *Server) GracefulStop() {
	s.server.GracefulStop()
}

func (s *Server) ValidateToken(
	ctx context.Context,
	request *iamv1.ValidateTokenRequest,
) (*iamv1.ValidateTokenResponse, error) {
	if request == nil || request.GetAccessToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "access token is required")
	}

	identityResult, err := s.validate.ValidateAccessToken(ctx, request.GetAccessToken())
	if errors.Is(err, identity.ErrMalformedAccessToken) {
		return nil, status.Error(codes.InvalidArgument, "access token is malformed")
	}
	if errors.Is(err, identity.ErrInvalidAccessToken) {
		return nil, status.Error(codes.Unauthenticated, "access token is invalid")
	}
	if err != nil {
		s.logger.Error().Err(err).Msg("token validation failed")
		return nil, status.Error(codes.Internal, "token validation failed")
	}

	accessLevel, err := toProtoAccessLevel(identityResult.AccessLevel)
	if err != nil {
		return nil, status.Error(codes.Internal, "access level is invalid")
	}

	return &iamv1.ValidateTokenResponse{
		UserId:                  identityResult.UserID,
		SubjectKind:             iamv1.SubjectKind_SUBJECT_KIND_USER,
		AccessLevel:             accessLevel,
		AuthenticationSessionId: identityResult.AuthenticationSessionID,
		ExpiresAt:               timestamppb.New(identityResult.ExpiresAt),
	}, nil
}

func toProtoAccessLevel(value string) (iamv1.AccessLevel, error) {
	switch value {
	case identity.AccessLevelStandard:
		return iamv1.AccessLevel_ACCESS_LEVEL_STANDARD, nil
	case identity.AccessLevelAdministrator:
		return iamv1.AccessLevel_ACCESS_LEVEL_ADMINISTRATOR, nil
	default:
		return iamv1.AccessLevel_ACCESS_LEVEL_UNSPECIFIED, fmt.Errorf("unknown access level")
	}
}
