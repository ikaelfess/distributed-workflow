package identity_test

import (
	"bytes"
	"testing"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOpaqueToken_HashesDeterministically(t *testing.T) {
	t.Parallel()

	seed := bytes.Repeat([]byte{0x42}, 32)
	raw, hash, err := identity.NewOpaqueToken(bytes.NewReader(seed))
	require.NoError(t, err)
	assert.NotEmpty(t, raw)
	assert.Len(t, hash, 32)

	again, err := identity.HashOpaqueToken(raw)
	require.NoError(t, err)
	assert.Equal(t, hash, again)
}

func TestHashOpaqueToken_RejectsMalformedInput(t *testing.T) {
	t.Parallel()

	_, err := identity.HashOpaqueToken("not-a-token")
	assert.Error(t, err)

	_, err = identity.HashOpaqueToken("")
	assert.Error(t, err)
}
