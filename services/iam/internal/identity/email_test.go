package identity_test

import (
	"testing"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalEmailAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "lowercases and trims",
			input: "  User@Example.COM ",
			want:  "user@example.com",
		},
		{
			name:    "rejects empty",
			input:   "   ",
			wantErr: true,
		},
		{
			name:    "rejects missing at",
			input:   "userexample.com",
			wantErr: true,
		},
		{
			name:    "rejects missing local part",
			input:   "@example.com",
			wantErr: true,
		},
		{
			name:    "rejects missing domain",
			input:   "user@",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := identity.CanonicalEmailAddress(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.String())
		})
	}
}
