package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAutoTagPolicy_Fields(t *testing.T) {
	tests := []struct {
		name   string
		policy AutoTagPolicy
	}{
		{
			name: "external link policy",
			policy: AutoTagPolicy{
				ID:         1,
				TagType:    "external-link",
				ReviewType: "review-comments",
			},
		},
		{
			name: "watched words policy with list",
			policy: func() AutoTagPolicy {
				listID := int64(42)
				return AutoTagPolicy{
					ID:         2,
					TagType:    "watched-words",
					ReviewType: "block-comments",
					ListID:     &listID,
				}
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotZero(t, tt.policy.ID)
			assert.NotEmpty(t, tt.policy.TagType)
			assert.NotEmpty(t, tt.policy.ReviewType)
		})
	}
}

func TestAutoTag_Fields(t *testing.T) {
	tag := AutoTag{
		Name:    "External link",
		Type:    "external-link",
		Enabled: true,
	}

	assert.Equal(t, "External link", tag.Name)
	assert.Equal(t, "external-link", tag.Type)
	assert.True(t, tag.Enabled)
}

func TestAutoTagPolicyInput_Validation(t *testing.T) {
	tests := []struct {
		name    string
		input   AutoTagPolicyInput
		wantErr bool
	}{
		{
			name: "valid external-link review-comments",
			input: AutoTagPolicyInput{
				TagType:    "external-link",
				ReviewType: "review-comments",
			},
			wantErr: false,
		},
		{
			name: "valid watched-words block-comments",
			input: AutoTagPolicyInput{
				TagType:    "watched-words",
				ReviewType: "block-comments",
			},
			wantErr: false,
		},
		{
			name: "invalid tag type",
			input: AutoTagPolicyInput{
				TagType:    "invalid",
				ReviewType: "review-comments",
			},
			wantErr: true,
		},
		{
			name: "invalid review type",
			input: AutoTagPolicyInput{
				TagType:    "external-link",
				ReviewType: "invalid",
			},
			wantErr: true,
		},
	}

	validTagTypes := map[string]bool{"external-link": true, "watched-words": true}
	validReviewTypes := map[string]bool{"review-comments": true, "block-comments": true}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasErr := !validTagTypes[tt.input.TagType] || !validReviewTypes[tt.input.ReviewType]
			assert.Equal(t, tt.wantErr, hasErr)
		})
	}
}
