package auth

import (
	"context"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type UserRepository interface {
	Create(ctx context.Context, user *User) error
	FindByEmail(ctx context.Context, email string) (*User, error)
	ExistsByEmail(ctx context.Context, email string) (bool, error)
}

type service struct {
	repo UserRepository
}

func NewAuthService(r UserRepository) Service {
	return &service{repo: r}
}

func (s *service) Register(ctx context.Context, payload RegisterRequest) (*User, error) {
	normalizedEmail := strings.ToLower(strings.TrimSpace(payload.Email))
	exists, err := s.repo.ExistsByEmail(ctx, normalizedEmail)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrEmailAlreadyTaken
	}
	if !payload.Role.Valid() {
		return nil, ErrInvalidRole
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(payload.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	user := &User{
		Email:        normalizedEmail,
		PasswordHash: string(hash),
		Role:         payload.Role,
	}

	if err := s.repo.Create(ctx, user); err != nil {
		return nil, err
	}

	return user, nil
}

func (s *service) Login(ctx context.Context, email, password string) (string, error) {
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	user, err := s.repo.FindByEmail(ctx, normalizedEmail)
	if err != nil || user == nil {
		return "", ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", ErrInvalidCredentials
	}

	// TODO: replace with real JWT
	token := "mock-token"

	return token, nil
}
