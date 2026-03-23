package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWatchedWordList_Fields(t *testing.T) {
	tests := []struct {
		name    string
		list    WatchedWordList
		wantNil bool // whether AccountName should be nil
		wantLen int  // expected word count
	}{
		{
			name: "server-level list",
			list: WatchedWordList{
				ID:        1,
				ListName:  "profanity",
				Words:     []string{"bad", "word"},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			wantNil: true,
			wantLen: 2,
		},
		{
			name: "account-level list",
			list: func() WatchedWordList {
				acct := "testuser"
				return WatchedWordList{
					ID:          2,
					AccountName: &acct,
					ListName:    "spam",
					Words:       []string{"buy", "now", "free"},
					CreatedAt:   time.Now(),
					UpdatedAt:   time.Now(),
				}
			}(),
			wantNil: false,
			wantLen: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantNil {
				assert.Nil(t, tt.list.AccountName)
			} else {
				assert.NotNil(t, tt.list.AccountName)
			}
			assert.Equal(t, tt.wantLen, len(tt.list.Words))
			assert.NotEmpty(t, tt.list.ListName)
		})
	}
}

func TestCreateWatchedWordListRequest_Validation(t *testing.T) {
	tests := []struct {
		name    string
		req     CreateWatchedWordListRequest
		wantErr bool
	}{
		{
			name: "valid request",
			req: CreateWatchedWordListRequest{
				ListName: "test list",
				Words:    []string{"word1", "word2"},
			},
			wantErr: false,
		},
		{
			name: "empty list name",
			req: CreateWatchedWordListRequest{
				ListName: "",
				Words:    []string{"word1"},
			},
			wantErr: true,
		},
		{
			name: "empty words",
			req: CreateWatchedWordListRequest{
				ListName: "test",
				Words:    []string{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasErr := tt.req.ListName == "" || len(tt.req.Words) == 0
			assert.Equal(t, tt.wantErr, hasErr)
		})
	}
}
