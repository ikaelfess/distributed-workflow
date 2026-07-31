package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
)

const problemBaseURL = "https://distributed-workflow.dev/problems/"

type ReadinessProbe interface {
	Ping(context.Context) error
}

type Handler struct {
	readiness ReadinessProbe
	register  *identity.RegisterService
	verify    *identity.VerifyService
	mux       *http.ServeMux
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

type Dependencies struct {
	Readiness ReadinessProbe
	Register  *identity.RegisterService
	Verify    *identity.VerifyService
}

func NewHandler(deps Dependencies) *Handler {
	h := &Handler{
		readiness: deps.Readiness,
		register:  deps.Register,
		verify:    deps.Verify,
		mux:       http.NewServeMux(),
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
	h.mux.HandleFunc("/", h.handleNotFound)

	return h
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

func (h *Handler) handleNotFound(w http.ResponseWriter, _ *http.Request) {
	writeProblem(
		w,
		http.StatusNotFound,
		"not-found",
		"not found",
		"the requested resource was not found",
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
