package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
)

const problemBaseURL = "https://distributed-workflow.dev/problems/"

type ReadinessProbe interface {
	Ping(context.Context) error
}

type Handler struct {
	readiness ReadinessProbe
	mux       *http.ServeMux
}

type healthResponse struct {
	Status string `json:"status"`
}

type problemDetails struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func NewHandler(readiness ReadinessProbe) *Handler {
	h := &Handler{
		readiness: readiness,
		mux:       http.NewServeMux(),
	}

	h.mux.HandleFunc("GET /v1/health/live", h.handleLiveness)
	h.mux.HandleFunc("HEAD /v1/health/live", h.handleMethodNotAllowed)
	h.mux.HandleFunc("/v1/health/live", h.handleMethodNotAllowed)
	h.mux.HandleFunc("GET /v1/health/ready", h.handleReadiness)
	h.mux.HandleFunc("HEAD /v1/health/ready", h.handleMethodNotAllowed)
	h.mux.HandleFunc("/v1/health/ready", h.handleMethodNotAllowed)
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

func (h *Handler) handleMethodNotAllowed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", http.MethodGet)
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
