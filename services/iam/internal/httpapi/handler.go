package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
)

const problemBaseURL = "https://distributed-workflow.dev/problems/"

type ReadinessProbe interface {
	Ping(context.Context) error
}

type Handler struct {
	readiness    ReadinessProbe
	register     *identity.RegisterService
	verify       *identity.VerifyService
	authenticate *identity.AuthenticateService
	refresh      *identity.RefreshService
	sessions     *identity.SessionService
	origins      OriginPolicy
	mux          *http.ServeMux
}

type healthResponse struct {
	Status string `json:"status"`
}

type acceptedResponse struct {
	Status string `json:"status"`
}

type problemDetails struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type registrationRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type verificationRequest struct {
	Challenge string `json:"challenge"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authenticationSessionResponse struct {
	ID             string `json:"id"`
	CreatedAt      string `json:"created_at"`
	LastUsedAt     string `json:"last_used_at"`
	ClientMetadata string `json:"client_metadata"`
	IP             string `json:"ip,omitempty"`
	Current        bool   `json:"current"`
}

type authenticationSessionListResponse struct {
	AuthenticationSessions []authenticationSessionResponse `json:"authentication_sessions"`
}

type Dependencies struct {
	Readiness    ReadinessProbe
	Register     *identity.RegisterService
	Verify       *identity.VerifyService
	Authenticate *identity.AuthenticateService
	Refresh      *identity.RefreshService
	Sessions     *identity.SessionService
	Origins      OriginPolicy
}

func NewHandler(deps Dependencies) http.Handler {
	h := &Handler{
		readiness:    deps.Readiness,
		register:     deps.Register,
		verify:       deps.Verify,
		authenticate: deps.Authenticate,
		refresh:      deps.Refresh,
		sessions:     deps.Sessions,
		origins:      deps.Origins,
		mux:          http.NewServeMux(),
	}

	h.mux.HandleFunc("GET /v1/health/live", h.handleLiveness)
	h.mux.HandleFunc("HEAD /v1/health/live", h.handleGetOnlyMethodNotAllowed)
	h.mux.HandleFunc("/v1/health/live", h.handleGetOnlyMethodNotAllowed)
	h.mux.HandleFunc("GET /v1/health/ready", h.handleReadiness)
	h.mux.HandleFunc("HEAD /v1/health/ready", h.handleGetOnlyMethodNotAllowed)
	h.mux.HandleFunc("/v1/health/ready", h.handleGetOnlyMethodNotAllowed)
	h.mux.HandleFunc("POST /v1/registrations", h.handleRegister)
	h.mux.HandleFunc("/v1/registrations", h.handlePostOnlyMethodNotAllowed)
	h.mux.HandleFunc("POST /v1/email-verifications", h.handleVerifyEmail)
	h.mux.HandleFunc("/v1/email-verifications", h.handlePostOnlyMethodNotAllowed)
	h.mux.HandleFunc("POST /v1/login", h.handleLogin)
	h.mux.HandleFunc("/v1/login", h.handlePostOnlyMethodNotAllowed)
	h.mux.HandleFunc("POST /v1/refresh", h.handleRefresh)
	h.mux.HandleFunc("/v1/refresh", h.handlePostOnlyMethodNotAllowed)
	h.mux.HandleFunc("POST /v1/logout", h.handleLogout)
	h.mux.HandleFunc("/v1/logout", h.handlePostOnlyMethodNotAllowed)
	h.mux.HandleFunc("GET /v1/authentication-sessions", h.handleListSessions)
	h.mux.HandleFunc(
		"DELETE /v1/authentication-sessions",
		h.handleRevokeAllSessions,
	)
	h.mux.HandleFunc(
		"DELETE /v1/authentication-sessions/{authenticationSessionId}",
		h.handleRevokeSession,
	)
	h.mux.HandleFunc("/v1/authentication-sessions", h.handleSessionsCollectionMethodNotAllowed)
	h.mux.HandleFunc(
		"/v1/authentication-sessions/{authenticationSessionId}",
		h.handleDeleteOnlyMethodNotAllowed,
	)
	h.mux.HandleFunc("/", h.handleNotFound)

	return withCORS(h, deps.Origins)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (h *Handler) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if err := h.readiness.Ping(r.Context()); err != nil {
		writeProblem(
			w,
			http.StatusServiceUnavailable,
			"service-unavailable",
			"service unavailable",
			"a required dependency is unavailable",
		)
		return
	}

	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	if h.register == nil {
		writeProblem(
			w,
			http.StatusServiceUnavailable,
			"service-unavailable",
			"service unavailable",
			"registration is unavailable",
		)
		return
	}

	var request registrationRequest
	if err := decodeJSON(r, &request); err != nil {
		writeProblem(
			w,
			http.StatusBadRequest,
			"invalid-request",
			"invalid request",
			"the request body is invalid",
		)
		return
	}

	err := h.register.Register(r.Context(), request.Email, request.Password)
	if errors.Is(err, identity.ErrInvalidRegistration) {
		writeProblem(
			w,
			http.StatusBadRequest,
			"invalid-registration",
			"invalid registration",
			"the registration request is invalid",
		)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "registration failed", "error", err)
		writeProblem(
			w,
			http.StatusInternalServerError,
			"internal-error",
			"internal error",
			"the registration request could not be completed",
		)
		return
	}

	writeJSON(w, http.StatusAccepted, acceptedResponse{Status: "accepted"})
}

func (h *Handler) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	if h.verify == nil {
		writeProblem(
			w,
			http.StatusServiceUnavailable,
			"service-unavailable",
			"service unavailable",
			"verification is unavailable",
		)
		return
	}

	var request verificationRequest
	if err := decodeJSON(r, &request); err != nil {
		writeProblem(
			w,
			http.StatusBadRequest,
			"invalid-request",
			"invalid request",
			"the request body is invalid",
		)
		return
	}

	err := h.verify.VerifyEmail(r.Context(), request.Challenge)
	if errors.Is(err, identity.ErrInvalidVerification) {
		writeProblem(
			w,
			http.StatusBadRequest,
			"invalid-verification",
			"invalid verification",
			"the verification challenge is invalid",
		)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "email verification failed", "error", err)
		writeProblem(
			w,
			http.StatusInternalServerError,
			"internal-error",
			"internal error",
			"the verification request could not be completed",
		)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if h.authenticate == nil {
		writeProblem(
			w,
			http.StatusServiceUnavailable,
			"service-unavailable",
			"service unavailable",
			"authentication is unavailable",
		)
		return
	}

	var request loginRequest
	if err := decodeJSON(r, &request); err != nil {
		writeProblem(
			w,
			http.StatusBadRequest,
			"invalid-request",
			"invalid request",
			"the request body is invalid",
		)
		return
	}

	credentials, err := h.authenticate.Authenticate(
		r.Context(),
		request.Email,
		request.Password,
		identity.SessionClientInfo{
			UserAgent: r.UserAgent(),
			IP:        clientIP(r),
		},
	)
	if errors.Is(err, identity.ErrAuthenticationFailed) {
		writeProblem(
			w,
			http.StatusUnauthorized,
			"authentication-failed",
			"authentication failed",
			"the email address or password is incorrect",
		)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "authentication request errored", "error", err)
		writeProblem(
			w,
			http.StatusInternalServerError,
			"internal-error",
			"internal error",
			"the authentication request could not be completed",
		)
		return
	}

	setCredentialCookies(w, credentials)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if h.refresh == nil {
		writeProblem(
			w,
			http.StatusServiceUnavailable,
			"service-unavailable",
			"service unavailable",
			"refresh is unavailable",
		)
		return
	}
	if !requireAllowedOrigin(w, r, h.origins) {
		return
	}
	csrfToken, ok := requireCSRFHeader(w, r)
	if !ok {
		return
	}

	refreshToken := cookieValue(r, RefreshTokenCookieName)
	if refreshToken == "" {
		writeProblem(
			w,
			http.StatusUnauthorized,
			"refresh-failed",
			"refresh failed",
			"the refresh request could not be completed",
		)
		return
	}

	credentials, err := h.refresh.Refresh(r.Context(), refreshToken, csrfToken)
	if errors.Is(err, identity.ErrInvalidCSRFToken) {
		writeProblem(
			w,
			http.StatusForbidden,
			"csrf-failed",
			"csrf failed",
			"the csrf token is missing or invalid",
		)
		return
	}
	if errors.Is(err, identity.ErrRefreshFailed) ||
		errors.Is(err, identity.ErrRefreshReuseDetected) {
		writeProblem(
			w,
			http.StatusUnauthorized,
			"refresh-failed",
			"refresh failed",
			"the refresh request could not be completed",
		)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "refresh request errored", "error", err)
		writeProblem(
			w,
			http.StatusInternalServerError,
			"internal-error",
			"internal error",
			"the refresh request could not be completed",
		)
		return
	}

	setCredentialCookies(w, credentials)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if h.sessions == nil {
		writeProblem(
			w,
			http.StatusServiceUnavailable,
			"service-unavailable",
			"service unavailable",
			"logout is unavailable",
		)
		return
	}
	if !requireAllowedOrigin(w, r, h.origins) {
		return
	}
	csrfToken, ok := requireCSRFHeader(w, r)
	if !ok {
		return
	}

	accessToken := cookieValue(r, AccessTokenCookieName)
	if accessToken == "" {
		writeUnauthorized(w)
		return
	}

	err := h.sessions.Logout(r.Context(), accessToken, csrfToken)
	if errors.Is(err, identity.ErrInvalidCSRFToken) {
		writeProblem(
			w,
			http.StatusForbidden,
			"csrf-failed",
			"csrf failed",
			"the csrf token is missing or invalid",
		)
		return
	}
	if errors.Is(err, identity.ErrInvalidAccessToken) ||
		errors.Is(err, identity.ErrAuthenticationSession) {
		writeUnauthorized(w)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "logout request errored", "error", err)
		writeProblem(
			w,
			http.StatusInternalServerError,
			"internal-error",
			"internal error",
			"the logout request could not be completed",
		)
		return
	}

	clearCredentialCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleListSessions(w http.ResponseWriter, r *http.Request) {
	if h.sessions == nil {
		writeProblem(
			w,
			http.StatusServiceUnavailable,
			"service-unavailable",
			"service unavailable",
			"authentication sessions are unavailable",
		)
		return
	}

	accessToken := cookieValue(r, AccessTokenCookieName)
	if accessToken == "" {
		writeUnauthorized(w)
		return
	}

	sessions, err := h.sessions.List(r.Context(), accessToken)
	if errors.Is(err, identity.ErrInvalidAccessToken) {
		writeUnauthorized(w)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "list sessions errored", "error", err)
		writeProblem(
			w,
			http.StatusInternalServerError,
			"internal-error",
			"internal error",
			"the authentication sessions could not be listed",
		)
		return
	}

	response := authenticationSessionListResponse{
		AuthenticationSessions: make([]authenticationSessionResponse, 0, len(sessions)),
	}
	for _, session := range sessions {
		item := authenticationSessionResponse{
			ID:             session.ID,
			CreatedAt:      session.CreatedAt.UTC().Format(time.RFC3339Nano),
			LastUsedAt:     session.LastUsedAt.UTC().Format(time.RFC3339Nano),
			ClientMetadata: session.ClientMetadata,
			Current:        session.Current,
		}
		if session.IP != nil {
			item.IP = session.IP.String()
		}
		response.AuthenticationSessions = append(response.AuthenticationSessions, item)
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) handleRevokeAllSessions(w http.ResponseWriter, r *http.Request) {
	if h.sessions == nil {
		writeProblem(
			w,
			http.StatusServiceUnavailable,
			"service-unavailable",
			"service unavailable",
			"authentication sessions are unavailable",
		)
		return
	}
	if !requireAllowedOrigin(w, r, h.origins) {
		return
	}
	csrfToken, ok := requireCSRFHeader(w, r)
	if !ok {
		return
	}

	accessToken := cookieValue(r, AccessTokenCookieName)
	if accessToken == "" {
		writeUnauthorized(w)
		return
	}

	err := h.sessions.RevokeAll(r.Context(), accessToken, csrfToken)
	if errors.Is(err, identity.ErrInvalidCSRFToken) {
		writeProblem(
			w,
			http.StatusForbidden,
			"csrf-failed",
			"csrf failed",
			"the csrf token is missing or invalid",
		)
		return
	}
	if errors.Is(err, identity.ErrInvalidAccessToken) {
		writeUnauthorized(w)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "revoke all sessions errored", "error", err)
		writeProblem(
			w,
			http.StatusInternalServerError,
			"internal-error",
			"internal error",
			"the authentication sessions could not be revoked",
		)
		return
	}

	clearCredentialCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	if h.sessions == nil {
		writeProblem(
			w,
			http.StatusServiceUnavailable,
			"service-unavailable",
			"service unavailable",
			"authentication sessions are unavailable",
		)
		return
	}
	if !requireAllowedOrigin(w, r, h.origins) {
		return
	}
	csrfToken, ok := requireCSRFHeader(w, r)
	if !ok {
		return
	}

	accessToken := cookieValue(r, AccessTokenCookieName)
	if accessToken == "" {
		writeUnauthorized(w)
		return
	}

	sessionID := r.PathValue("authenticationSessionId")
	revokedCurrent, err := h.sessions.RevokeOne(
		r.Context(),
		accessToken,
		csrfToken,
		sessionID,
	)
	if errors.Is(err, identity.ErrInvalidCSRFToken) {
		writeProblem(
			w,
			http.StatusForbidden,
			"csrf-failed",
			"csrf failed",
			"the csrf token is missing or invalid",
		)
		return
	}
	if errors.Is(err, identity.ErrInvalidAccessToken) {
		writeUnauthorized(w)
		return
	}
	if errors.Is(err, identity.ErrSessionNotFound) {
		writeProblem(
			w,
			http.StatusNotFound,
			"not-found",
			"not found",
			"the authentication session was not found",
		)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "revoke session errored", "error", err)
		writeProblem(
			w,
			http.StatusInternalServerError,
			"internal-error",
			"internal error",
			"the authentication session could not be revoked",
		)
		return
	}

	if revokedCurrent {
		clearCredentialCookies(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

func clientIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return netip.Addr{}
	}
	return addr
}

func (h *Handler) handleGetOnlyMethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", http.MethodGet)
	writeProblem(
		w,
		http.StatusMethodNotAllowed,
		"method-not-allowed",
		"method not allowed",
		"the requested method is not supported for this resource",
	)
}

func (h *Handler) handlePostOnlyMethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", http.MethodPost)
	writeProblem(
		w,
		http.StatusMethodNotAllowed,
		"method-not-allowed",
		"method not allowed",
		"the requested method is not supported for this resource",
	)
}

func (h *Handler) handleDeleteOnlyMethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", http.MethodDelete)
	writeProblem(
		w,
		http.StatusMethodNotAllowed,
		"method-not-allowed",
		"method not allowed",
		"the requested method is not supported for this resource",
	)
}

func (h *Handler) handleSessionsCollectionMethodNotAllowed(
	w http.ResponseWriter,
	_ *http.Request,
) {
	w.Header().Set("Allow", http.MethodGet+", "+http.MethodDelete)
	writeProblem(
		w,
		http.StatusMethodNotAllowed,
		"method-not-allowed",
		"method not allowed",
		"the requested method is not supported for this resource",
	)
}

func (h *Handler) handleNotFound(w http.ResponseWriter, _ *http.Request) {
	writeProblem(
		w,
		http.StatusNotFound,
		"not-found",
		"not found",
		"the requested resource was not found",
	)
}

func writeUnauthorized(w http.ResponseWriter) {
	writeProblem(
		w,
		http.StatusUnauthorized,
		"authentication-required",
		"authentication required",
		"a valid access token is required",
	)
}

func decodeJSON(r *http.Request, value any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single json object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	writeResponse(w, status, "application/json", value)
}

func writeProblem(w http.ResponseWriter, status int, problemType, title, detail string) {
	writeResponse(w, status, "application/problem+json", problemDetails{
		Type:   problemBaseURL + problemType,
		Title:  title,
		Status: status,
		Detail: detail,
	})
}

func writeResponse(w http.ResponseWriter, status int, contentType string, value any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}
