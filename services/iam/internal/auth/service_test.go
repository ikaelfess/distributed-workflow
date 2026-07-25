package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

type mockUserRepository struct {
	createFunc        func(ctx context.Context, user *User) error
	findByEmailFunc   func(ctx context.Context, email string) (*User, error)
	existsByEmailFunc func(ctx context.Context, email string) (bool, error)
}

func (m *mockUserRepository) Create(ctx context.Context, user *User) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, user)
	}
	return nil
}

func (m *mockUserRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	if m.findByEmailFunc != nil {
		return m.findByEmailFunc(ctx, email)
	}
	return nil, nil
}

func (m *mockUserRepository) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	if m.existsByEmailFunc != nil {
		return m.existsByEmailFunc(ctx, email)
	}
	return false, nil
}

func TestService_Register(t *testing.T) {
	t.Run("creates user with normalized email hashed password and role", func(t *testing.T) {
		var createdUser *User

		repo := &mockUserRepository{
			existsByEmailFunc: func(ctx context.Context, email string) (bool, error) {
				assert.Equal(t, "admin@example.com", email)
				return false, nil
			},
			createFunc: func(ctx context.Context, user *User) error {
				createdUser = user
				user.ID = "user-id"
				return nil
			},
		}

		svc := NewAuthService(repo)
		result, err := svc.Register(context.Background(), RegisterRequest{
			Email:    "  Admin@Example.com  ",
			Password: "password123",
			Role:     AdminRole,
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, createdUser)
		assert.Equal(t, "user-id", result.ID)
		assert.Equal(t, "admin@example.com", result.Email)
		assert.Equal(t, AdminRole, result.Role)
		assert.Equal(t, "admin@example.com", createdUser.Email)
		assert.Equal(t, AdminRole, createdUser.Role)
		assert.NotEmpty(t, createdUser.PasswordHash)
		assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(createdUser.PasswordHash), []byte("password123")))
	})

	t.Run("rejects invalid role before create", func(t *testing.T) {
		createCalled := false

		repo := &mockUserRepository{
			existsByEmailFunc: func(ctx context.Context, email string) (bool, error) {
				return false, nil
			},
			createFunc: func(ctx context.Context, user *User) error {
				createCalled = true
				return nil
			},
		}

		svc := NewAuthService(repo)
		result, err := svc.Register(context.Background(), RegisterRequest{
			Email:    "user@example.com",
			Password: "password123",
			Role:     Role("owner"),
		})

		require.ErrorIs(t, err, ErrInvalidRole)
		assert.Nil(t, result)
		assert.False(t, createCalled)
	})

	t.Run("returns email taken error without create", func(t *testing.T) {
		createCalled := false

		repo := &mockUserRepository{
			existsByEmailFunc: func(ctx context.Context, email string) (bool, error) {
				return true, nil
			},
			createFunc: func(ctx context.Context, user *User) error {
				createCalled = true
				return nil
			},
		}

		svc := NewAuthService(repo)
		result, err := svc.Register(context.Background(), RegisterRequest{
			Email:    "user@example.com",
			Password: "password123",
			Role:     UserRole,
		})

		require.ErrorIs(t, err, ErrEmailAlreadyTaken)
		assert.Nil(t, result)
		assert.False(t, createCalled)
	})

	t.Run("propagates repository exists error", func(t *testing.T) {
		repo := &mockUserRepository{
			existsByEmailFunc: func(ctx context.Context, email string) (bool, error) {
				return false, errors.New("db down")
			},
		}

		svc := NewAuthService(repo)
		result, err := svc.Register(context.Background(), RegisterRequest{
			Email:    "user@example.com",
			Password: "password123",
			Role:     UserRole,
		})

		require.EqualError(t, err, "db down")
		assert.Nil(t, result)
	})
}
