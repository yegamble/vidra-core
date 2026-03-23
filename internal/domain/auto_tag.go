package domain

// AutoTagPolicy represents a policy for automatically tagging content.
type AutoTagPolicy struct {
	ID          int64   `json:"id" db:"id"`
	AccountName *string `json:"accountName,omitempty" db:"account_name"`
	TagType     string  `json:"type" db:"tag_type"`      // "external-link", "watched-words"
	ReviewType  string  `json:"review" db:"review_type"` // "review-comments", "block-comments"
	ListID      *int64  `json:"listId,omitempty" db:"list_id"`
}

// AutoTag represents an available automatic tag type with its status.
type AutoTag struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // "external-link", "watched-words"
	Enabled bool   `json:"enabled"`
}

// UpdateAutoTagPoliciesRequest represents a request to update auto-tag policies.
type UpdateAutoTagPoliciesRequest struct {
	Policies []AutoTagPolicyInput `json:"policies" validate:"required"`
}

// AutoTagPolicyInput represents a single policy in an update request.
type AutoTagPolicyInput struct {
	TagType    string `json:"type" validate:"required,oneof=external-link watched-words"`
	ReviewType string `json:"review" validate:"required,oneof=review-comments block-comments"`
	ListID     *int64 `json:"listId,omitempty"`
}
