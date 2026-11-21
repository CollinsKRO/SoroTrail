package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateAPIKey_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		testFn func(t *testing.T)
	}{
		{
			name: "generates valid keys successfully",
			testFn: func(t *testing.T) {
				plaintext, prefix, digest, err := GenerateAPIKey()
				require.NoError(t, err)
				assert.NotEmpty(t, plaintext)
				assert.NotEmpty(t, prefix)
				assert.NotEmpty(t, digest)
			},
		},
		{
			name: "multiple sequential calls produce unique keys and prefixes",
			testFn: func(t *testing.T) {
				pt1, pre1, dig1, err1 := GenerateAPIKey()
				pt2, pre2, dig2, err2 := GenerateAPIKey()
				require.NoError(t, err1)
				require.NoError(t, err2)

				assert.NotEqual(t, pt1, pt2)
				assert.NotEqual(t, pre1, pre2)
				assert.NotEqual(t, dig1, dig2)
			},
		},
		{
			name: "parseAPIKey handles edge cases and invalid formats",
			testFn: func(t *testing.T) {
				// Empty input
				p, d, ok := ParseAPIKey("")
				assert.False(t, ok)
				assert.Empty(t, p)
				assert.Nil(t, d)

				// Boundary / Invalid inputs: missing parts, wrong scheme, invalid prefix length
				invalidKeys := []string(
					[]string{
						"st",
						"st_",
						"st_shortprefix",
						"wrongscheme_1234567890abcdef_secret",
						"st_1234567890abcdef_", // missing secret
					},
				)
				for _, ik := range invalidKeys {
					_, _, valid := ParseAPIKey(ik)
					assert.False(t, valid, "expected %q to be invalid", ik)
				}
			},
		},
		{
			name: "validates correct api key format",
			testFn: func(t *testing.T) {
				plaintext, _, _, err := GenerateAPIKey()
				require.NoError(t, err)
				p, _, valid := ParseAPIKey(plaintext)
				assert.True(t, valid)
				assert.NotEmpty(t, p)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.testFn)
	}
}
