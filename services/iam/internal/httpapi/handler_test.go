package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/httpapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type readinessProbeFunc func(context.Context) error

func (f readinessProbeFunc) Ping(ctx context.Context) error {
	return f(ctx)
}

func TestHandler_Health(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		path           string
		probe          readinessProbeFunc
		expectedStatus int
		expectedType   string
	}{
		{
			name: "liveness does not depend on postgres",
			path: "/v1/health/live",
			probe: func(context.Context) error {
				return errors.New("postgres unavailable")
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "readiness succeeds when postgres responds",
			path: "/v1/health/ready",
			probe: func(context.Context) error {
				return nil
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "readiness fails when postgres is unavailable",
			path: "/v1/health/ready",
			probe: func(context.Context) error {
				return errors.New("postgres unavailable")
			},
			expectedStatus: http.StatusServiceUnavailable,
			expectedType:   "https://distributed-workflow.dev/problems/service-unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(httpapi.NewHandler(tt.probe))
			defer server.Close()

			response, err := server.Client().Get(server.URL + tt.path)
			require.NoError(t, err)
			defer response.Body.Close()

			assert.Equal(t, tt.expectedStatus, response.StatusCode)

			if tt.expectedStatus == http.StatusOK {
				assert.Equal(t, "application/json", response.Header.Get("Content-Type"))

				var health struct {
					Status string `json:"status"`
				}
				require.NoError(t, json.NewDecoder(response.Body).Decode(&health))
				assert.Equal(t, "ok", health.Status)
				return
			}

			assert.Equal(t, "application/problem+json", response.Header.Get("Content-Type"))

			var problem struct {
				Type   string `json:"type"`
				Title  string `json:"title"`
				Status int    `json:"status"`
			}
			require.NoError(t, json.NewDecoder(response.Body).Decode(&problem))
			assert.Equal(t, tt.expectedType, problem.Type)
			assert.Equal(t, "service unavailable", problem.Title)
			assert.Equal(t, tt.expectedStatus, problem.Status)
		})
	}
}

func TestHandler_ProblemDetails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
		expectedTitle  string
		expectedAllow  string
	}{
		{
			name:           "unknown route",
			method:         http.MethodGet,
			path:           "/v1/unknown",
			expectedStatus: http.StatusNotFound,
			expectedTitle:  "not found",
		},
		{
			name:           "unsupported method",
			method:         http.MethodPost,
			path:           "/v1/health/live",
			expectedStatus: http.StatusMethodNotAllowed,
			expectedTitle:  "method not allowed",
			expectedAllow:  http.MethodGet,
		},
		{
			name:           "head is not declared by the contract",
			method:         http.MethodHead,
			path:           "/v1/health/ready",
			expectedStatus: http.StatusMethodNotAllowed,
			expectedTitle:  "method not allowed",
			expectedAllow:  http.MethodGet,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(httpapi.NewHandler(readinessProbeFunc(func(context.Context) error {
				return nil
			})))
			defer server.Close()

			request, err := http.NewRequest(tt.method, server.URL+tt.path, nil)
			require.NoError(t, err)

			response, err := server.Client().Do(request)
			require.NoError(t, err)
			defer response.Body.Close()

			assert.Equal(t, tt.expectedStatus, response.StatusCode)
			assert.Equal(t, "application/problem+json", response.Header.Get("Content-Type"))
			assert.Equal(t, tt.expectedAllow, response.Header.Get("Allow"))

			if tt.method == http.MethodHead {
				return
			}

			var problem struct {
				Title  string `json:"title"`
				Status int    `json:"status"`
			}
			require.NoError(t, json.NewDecoder(response.Body).Decode(&problem))
			assert.Equal(t, tt.expectedTitle, problem.Title)
			assert.Equal(t, tt.expectedStatus, problem.Status)
		})
	}
}
