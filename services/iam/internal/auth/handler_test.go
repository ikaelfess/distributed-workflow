package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

type mockAuthService struct {
	registerFunc func(ctx context.Context, payload RegisterRequest) (*User, error)
	loginFunc    func(ctx context.Context, email, password string) (string, error)
}

func (m *mockAuthService) Register(ctx context.Context, payload RegisterRequest) (*User, error) {
	if m.registerFunc != nil {
		return m.registerFunc(ctx, payload)
	}
	return nil, nil
}

func (m *mockAuthService) Login(ctx context.Context, email, password string) (string, error) {
	if m.loginFunc != nil {
		return m.loginFunc(ctx, email, password)
	}
	return "", nil
}

func setupRouter(handler *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	auth := router.Group(GroupRoute)
	auth.POST(RegisterRoute, handler.Register)
	auth.POST(LoginRoute, handler.Login)
	return router
}

func TestHandler_Register(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    any
		mockUser       *User
		mockError      error
		expectedStatus int
		expectResponse bool
		assertPayload  func(t *testing.T, payload RegisterRequest)
	}{
		{
			name: "successful registration with valid email password and role",
			requestBody: RegisterRequest{
				Email:    "user@example.com",
				Password: "password123",
				Role:     AdminRole,
			},
			mockUser: &User{
				ID:    "uuid-user",
				Email: "user@example.com",
				Role:  AdminRole,
			},
			expectedStatus: http.StatusCreated,
			expectResponse: true,
			assertPayload: func(t *testing.T, payload RegisterRequest) {
				assert.Equal(t, RegisterRequest{
					Email:    "user@example.com",
					Password: "password123",
					Role:     AdminRole,
				}, payload)
			},
		},
		{
			name:           "fail - invalid JSON body",
			requestBody:    "invalid json",
			expectedStatus: http.StatusBadRequest,
			expectResponse: false,
		},
		{
			name: "fail - missing email field",
			requestBody: map[string]string{
				"password": "password123",
			},
			expectedStatus: http.StatusBadRequest,
			expectResponse: false,
		},
		{
			name: "fail - missing password field",
			requestBody: map[string]string{
				"email": "user@example.com",
			},
			expectedStatus: http.StatusBadRequest,
			expectResponse: false,
		},
		{
			name: "fail - invalid email format",
			requestBody: map[string]string{
				"email":    "invalid-email",
				"password": "password123",
			},
			expectedStatus: http.StatusBadRequest,
			expectResponse: false,
		},
		{
			name: "fail - password too short",
			requestBody: map[string]string{
				"email":    "user@example.com",
				"password": "short",
			},
			expectedStatus: http.StatusBadRequest,
			expectResponse: false,
		},
		{
			name: "fail - missing role field",
			requestBody: map[string]string{
				"email":    "user@example.com",
				"password": "password123",
			},
			expectedStatus: http.StatusBadRequest,
			expectResponse: false,
		},
		{
			name: "fail - email already taken",
			requestBody: RegisterRequest{
				Email:    "existing@example.com",
				Password: "password123",
				Role:     UserRole,
			},
			mockError:      ErrEmailAlreadyTaken,
			expectedStatus: http.StatusInternalServerError,
			expectResponse: false,
		},
		{
			name: "fail - service returns generic error",
			requestBody: RegisterRequest{
				Email:    "user@example.com",
				Password: "password123",
				Role:     UserRole,
			},
			mockError:      errors.New("database connection failed"),
			expectedStatus: http.StatusInternalServerError,
			expectResponse: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := &mockAuthService{
				registerFunc: func(ctx context.Context, payload RegisterRequest) (*User, error) {
					if tt.assertPayload != nil {
						tt.assertPayload(t, payload)
					}
					return tt.mockUser, tt.mockError
				},
			}

			handler := NewAuthHandler(mockService)
			router := setupRouter(handler)

			var body []byte
			var err error
			switch v := tt.requestBody.(type) {
			case string:
				body = []byte(v)
			default:
				body, err = json.Marshal(v)
				assert.NoError(t, err)
			}

			req := httptest.NewRequest(http.MethodPost, FullRegisterRoute, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectResponse {
				var response RegisterResponse
				err = json.Unmarshal(w.Body.Bytes(), &response)
				assert.NoError(t, err)
				assert.Equal(t, tt.mockUser.ID, response.ID)
				assert.Equal(t, tt.mockUser.Email, response.Email)
				assert.Equal(t, tt.mockUser.Role, response.Role)
			}
		})
	}
}

func TestHandler_Login(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    any
		mockToken      string
		mockError      error
		expectedStatus int
		expectResponse bool
	}{
		{
			name: "successful login with valid credentials",
			requestBody: LoginRequest{
				Email:    "user@example.com",
				Password: "password123",
			},
			mockToken:      "valid-jwt-token",
			expectedStatus: http.StatusOK,
			expectResponse: true,
		},
		{
			name:           "fail - invalid JSON body",
			requestBody:    "invalid json",
			expectedStatus: http.StatusBadRequest,
			expectResponse: false,
		},
		{
			name: "fail - missing email field",
			requestBody: map[string]string{
				"password": "password123",
			},
			expectedStatus: http.StatusBadRequest,
			expectResponse: false,
		},
		{
			name: "fail - missing password field",
			requestBody: map[string]string{
				"email": "user@example.com",
			},
			expectedStatus: http.StatusBadRequest,
			expectResponse: false,
		},
		{
			name: "fail - invalid email format",
			requestBody: map[string]string{
				"email":    "invalid-email",
				"password": "password123",
			},
			expectedStatus: http.StatusBadRequest,
			expectResponse: false,
		},
		{
			name: "fail - invalid credentials",
			requestBody: LoginRequest{
				Email:    "user@example.com",
				Password: "wrongpassword",
			},
			mockError:      ErrInvalidCredentials,
			expectedStatus: http.StatusUnauthorized,
			expectResponse: false,
		},
		{
			name: "fail - service returns generic error",
			requestBody: LoginRequest{
				Email:    "user@example.com",
				Password: "password123",
			},
			mockError:      errors.New("database connection failed"),
			expectedStatus: http.StatusInternalServerError,
			expectResponse: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := &mockAuthService{
				loginFunc: func(ctx context.Context, email, password string) (string, error) {
					return tt.mockToken, tt.mockError
				},
			}

			handler := NewAuthHandler(mockService)
			router := setupRouter(handler)

			var body []byte
			var err error
			switch v := tt.requestBody.(type) {
			case string:
				body = []byte(v)
			default:
				body, err = json.Marshal(v)
				assert.NoError(t, err)
			}

			req := httptest.NewRequest(http.MethodPost, FullLoginRoute, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectResponse {
				var response LoginResponse
				err = json.Unmarshal(w.Body.Bytes(), &response)
				assert.NoError(t, err)
				assert.Equal(t, tt.mockToken, response.Token)
			}
		})
	}
}
