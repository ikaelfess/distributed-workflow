package identity_test

import (
	"strings"
	"testing"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasswordHasher(t *testing.T) {
	t.Parallel()

	hasher := identity.NewPasswordHasher(identity.PasswordPolicy{
		Time:    1,
		Memory:  64 * 1024,
		Threads: 1,
		KeyLen:  32,
		SaltLen: 16,
	})

	t.Run("hashes and verifies argon2id", func(t *testing.T) {
		t.Parallel()

		encoded, err := hasher.Hash("correct horse battery staple")
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(encoded, "$argon2id$"))
		assert.True(t, hasher.Verify(encoded, "correct horse battery staple"))
		assert.False(t, hasher.Verify(encoded, "wrong password!!"))
	})

	t.Run("rejects short passwords", func(t *testing.T) {
		t.Parallel()

		_, err := hasher.Hash(strings.Repeat("a", 11))
		require.ErrorIs(t, err, identity.ErrPasswordTooShort)
	})

	t.Run("rejects long passwords", func(t *testing.T) {
		t.Parallel()

		_, err := hasher.Hash(strings.Repeat("a", 129))
		require.ErrorIs(t, err, identity.ErrPasswordTooLong)
	})

	t.Run("accepts boundary lengths", func(t *testing.T) {
		t.Parallel()

		for _, password := range []string{strings.Repeat("a", 12), strings.Repeat("b", 128)} {
			encoded, err := hasher.Hash(password)
			require.NoError(t, err)
			assert.True(t, hasher.Verify(encoded, password))
		}
	})
}
