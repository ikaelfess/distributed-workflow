package auth

import (
	"time"

	"github.com/uptrace/bun"
)

type Role string

const (
	UserRole  Role = "user"
	AdminRole Role = "admin"
)

type User struct {
	bun.BaseModel `bun:"table:users"`

	ID           string `bun:"id,pk,type:uuid,default:gen_random_uuid()"`
	Email        string `bun:"email,unique,notnull"`
	Role         Role   `bun:"role,notnull,default:'user'"`
	PasswordHash string `bun:"password_hash,notnull"`

	CreatedAt time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp"`
	UpdatedAt time.Time `bun:"updated_at,nullzero,notnull,default:current_timestamp"`
}

func (r Role) Valid() bool {
	switch r {
	case UserRole, AdminRole:
		return true
	default:
		return false
	}
}
