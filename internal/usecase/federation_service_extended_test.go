package usecase

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrPtrIf(t *testing.T) {
	t.Run("true returns pointer", func(t *testing.T) {
		result := strPtrIf(true, "hello")
		require.NotNil(t, result)
		assert.Equal(t, "hello", *result)
	})

	t.Run("false returns nil", func(t *testing.T) {
		result := strPtrIf(false, "hello")
		assert.Nil(t, result)
	})
}

func TestGetMaxItems(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		svc := &federationService{cfg: &config.Config{}}
		assert.Equal(t, 40, svc.getMaxItems())
	})

	t.Run("configured", func(t *testing.T) {
		svc := &federationService{cfg: &config.Config{FederationIngestMaxItems: 100}}
		assert.Equal(t, 100, svc.getMaxItems())
	})
}

func TestGetMaxPages(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		svc := &federationService{cfg: &config.Config{}}
		assert.Equal(t, 2, svc.getMaxPages())
	})

	t.Run("configured", func(t *testing.T) {
		svc := &federationService{cfg: &config.Config{FederationIngestMaxPages: 5}}
		assert.Equal(t, 5, svc.getMaxPages())
	})
}

func TestCalculatePageLimit(t *testing.T) {
	svc := &federationService{}

	t.Run("normal case", func(t *testing.T) {
		result := svc.calculatePageLimit(40, 0)
		assert.Equal(t, 20, result)
	})

	t.Run("remaining less than page limit", func(t *testing.T) {
		result := svc.calculatePageLimit(40, 35)
		assert.Equal(t, 5, result)
	})

	t.Run("remaining equals page limit", func(t *testing.T) {
		result := svc.calculatePageLimit(40, 20)
		assert.Equal(t, 20, result)
	})
}

func TestBuildFederatedPost(t *testing.T) {
	svc := &federationService{cfg: &config.Config{}}

	t.Run("full data", func(t *testing.T) {
		m := map[string]any{
			"post": map[string]any{
				"uri": "at://did:plc:abc/app.bsky.feed.post/123",
				"cid": "bafyreiabc",
				"author": map[string]any{
					"did":    "did:plc:abc",
					"handle": "test.bsky.social",
				},
				"record": map[string]any{
					"text":      "Hello world",
					"createdAt": "2024-01-15T12:00:00Z",
				},
			},
		}
		post := m["post"].(map[string]any)
		rec := post["record"].(map[string]any)

		result := svc.buildFederatedPost(m, post, rec)

		require.NotNil(t, result)
		assert.Equal(t, "at://did:plc:abc/app.bsky.feed.post/123", result.URI)
		assert.Equal(t, "did:plc:abc", result.ActorDID)
		require.NotNil(t, result.Text)
		assert.Equal(t, "Hello world", *result.Text)
		require.NotNil(t, result.CID)
		assert.Equal(t, "bafyreiabc", *result.CID)
		require.NotNil(t, result.ActorHandle)
		assert.Equal(t, "test.bsky.social", *result.ActorHandle)
		require.NotNil(t, result.CreatedAt)
	})

	t.Run("minimal data", func(t *testing.T) {
		m := map[string]any{}
		post := map[string]any{}
		rec := map[string]any{}

		result := svc.buildFederatedPost(m, post, rec)

		require.NotNil(t, result)
		assert.Equal(t, "", result.URI)
		assert.Equal(t, "", result.ActorDID)
		assert.Nil(t, result.Text)
		assert.Nil(t, result.CID)
		assert.Nil(t, result.CreatedAt)
	})

	t.Run("with external embed", func(t *testing.T) {
		m := map[string]any{}
		post := map[string]any{
			"uri": "at://did:plc:abc/post/1",
		}
		rec := map[string]any{
			"text": "Check this out",
			"embed": map[string]any{
				"$type": "app.bsky.embed.external",
				"external": map[string]any{
					"uri":         "https://example.com",
					"title":       "Example",
					"description": "An example site",
				},
			},
		}

		result := svc.buildFederatedPost(m, post, rec)

		require.NotNil(t, result)
		require.NotNil(t, result.EmbedType)
		assert.Equal(t, "external", *result.EmbedType)
		require.NotNil(t, result.EmbedURL)
		assert.Equal(t, "https://example.com", *result.EmbedURL)
		require.NotNil(t, result.EmbedTitle)
		assert.Equal(t, "Example", *result.EmbedTitle)
	})

	t.Run("with labels", func(t *testing.T) {
		m := map[string]any{}
		post := map[string]any{
			"labels": map[string]any{
				"values": []any{
					map[string]any{"val": "nsfw"},
				},
			},
		}
		rec := map[string]any{"text": "labeled post"}

		result := svc.buildFederatedPost(m, post, rec)

		require.NotNil(t, result)
		assert.NotEmpty(t, result.Labels)
	})
}

func TestProcessItem(t *testing.T) {
	svc := &federationService{
		cfg:  &config.Config{},
		repo: &mockFederationRepo{},
	}
	blockedSet := map[string]struct{}{}

	t.Run("nil item", func(t *testing.T) {
		result := svc.processItem(context.Background(), nil, blockedSet)
		assert.False(t, result)
	})

	t.Run("missing post", func(t *testing.T) {
		item := map[string]any{}
		result := svc.processItem(context.Background(), item, blockedSet)
		assert.False(t, result)
	})

	t.Run("missing record", func(t *testing.T) {
		item := map[string]any{
			"post": map[string]any{
				"uri": "at://did:plc:abc/post/1",
			},
		}
		result := svc.processItem(context.Background(), item, blockedSet)
		assert.False(t, result)
	})

	t.Run("blocked label", func(t *testing.T) {
		blocked := map[string]struct{}{"nsfw": {}}
		item := map[string]any{
			"post": map[string]any{
				"uri": "at://did:plc:abc/post/1",
				"labels": map[string]any{
					"values": []any{
						map[string]any{"val": "nsfw"},
					},
				},
				"record": map[string]any{"text": "blocked"},
			},
		}
		result := svc.processItem(context.Background(), item, blocked)
		assert.False(t, result)
	})

	t.Run("success", func(t *testing.T) {
		repo := &mockFederationRepo{}
		svc := &federationService{
			cfg:  &config.Config{},
			repo: repo,
		}

		item := map[string]any{
			"post": map[string]any{
				"uri": "at://did:plc:abc/post/1",
				"cid": "bafyreiabc",
				"author": map[string]any{
					"did":    "did:plc:abc",
					"handle": "test.bsky.social",
				},
				"record": map[string]any{
					"text":      "Hello world",
					"createdAt": "2024-01-15T12:00:00Z",
				},
			},
		}
		result := svc.processItem(context.Background(), item, blockedSet)
		assert.True(t, result)
		assert.Len(t, repo.posts, 1)
	})
}

func TestProcessPageItems(t *testing.T) {
	repo := &mockFederationRepo{}
	svc := &federationService{
		cfg:  &config.Config{},
		repo: repo,
	}
	blockedSet := map[string]struct{}{}

	items := []any{
		map[string]any{
			"post": map[string]any{
				"uri":    "at://did:plc:abc/post/1",
				"author": map[string]any{"did": "did:plc:abc"},
				"record": map[string]any{"text": "post 1"},
			},
		},
		map[string]any{},
		map[string]any{
			"post": map[string]any{
				"uri":    "at://did:plc:abc/post/2",
				"author": map[string]any{"did": "did:plc:abc"},
				"record": map[string]any{"text": "post 2"},
			},
		},
	}

	count := svc.processPageItems(context.Background(), items, blockedSet)
	assert.Equal(t, 2, count)
}

func TestGetActorCursor_NilModRepo(t *testing.T) {
	svc := &federationService{modRepo: nil}
	assert.Equal(t, "", svc.getActorCursor(context.Background(), "test"))
}

func TestSetActorCursor_NilModRepo(t *testing.T) {
	svc := &federationService{modRepo: nil}
	err := svc.setActorCursor(context.Background(), "test", "cursor123")
	assert.NoError(t, err)
}

func TestGetActorNextAt_NilModRepo(t *testing.T) {
	svc := &federationService{modRepo: nil}
	result := svc.getActorNextAt(context.Background(), "test")
	assert.True(t, result.IsZero())
}

func TestSetActorNextAt_NilModRepo(t *testing.T) {
	svc := &federationService{modRepo: nil}
	svc.setActorNextAt(context.Background(), "test", time.Now())
}

func TestGetActorAttempts_NilModRepo(t *testing.T) {
	svc := &federationService{modRepo: nil}
	assert.Equal(t, 0, svc.getActorAttempts(context.Background(), "test"))
}

func TestSetActorAttempts_NilModRepo(t *testing.T) {
	svc := &federationService{modRepo: nil}
	svc.setActorAttempts(context.Background(), "test", 5)
}

func TestBumpActorBackoff_NilModRepo(t *testing.T) {
	svc := &federationService{modRepo: nil}
	svc.bumpActorBackoff(context.Background(), "test")
}

func TestResetActorBackoff(t *testing.T) {
	modRepo := &mockModRepo{configs: map[string]*domain.InstanceConfig{}}
	svc := &federationService{modRepo: modRepo}
	svc.resetActorBackoff(context.Background(), "actor1")
	cfg, ok := modRepo.configs["atproto_actor_actor1_attempts"]
	require.True(t, ok)
	var n int
	_ = json.Unmarshal(cfg.Value, &n)
	assert.Equal(t, 0, n)
}

func TestGetActorCursor_TableAware(t *testing.T) {
	t.Run("uses repo first", func(t *testing.T) {
		repo := &mockFederationRepo{
			actorStates: map[string]*actorState{
				"actor1": {cursor: "cursor-from-repo"},
			},
		}
		svc := &federationService{repo: repo}
		result := svc.getActorCursor(context.Background(), "actor1")
		assert.Equal(t, "cursor-from-repo", result)
	})

	t.Run("falls back to modRepo", func(t *testing.T) {
		modRepo := &mockModRepo{
			configs: map[string]*domain.InstanceConfig{
				"atproto_cursor_actor1": {
					Value: json.RawMessage(`"cursor-from-config"`),
				},
			},
		}
		svc := &federationService{
			repo:    nil,
			modRepo: modRepo,
		}
		result := svc.getActorCursor(context.Background(), "actor1")
		assert.Equal(t, "cursor-from-config", result)
	})
}

func TestSetActorCursor_TableAware(t *testing.T) {
	repo := &mockFederationRepo{actorStates: map[string]*actorState{}}
	modRepo := &mockModRepo{configs: map[string]*domain.InstanceConfig{}}
	svc := &federationService{repo: repo, modRepo: modRepo}

	err := svc.setActorCursor(context.Background(), "actor1", "new-cursor")
	assert.NoError(t, err)
	assert.Equal(t, "new-cursor", repo.actorStates["actor1"].cursor)
}

func TestGetActorCursor_WithModRepo(t *testing.T) {
	modRepo := &mockModRepo{
		configs: map[string]*domain.InstanceConfig{
			"atproto_cursor_actor1": {
				Value: json.RawMessage(`"saved-cursor"`),
			},
		},
	}
	svc := &federationService{modRepo: modRepo}
	result := svc.getActorCursor(context.Background(), "actor1")
	assert.Equal(t, "saved-cursor", result)
}

func TestSetActorCursor_WithModRepo(t *testing.T) {
	modRepo := &mockModRepo{configs: map[string]*domain.InstanceConfig{}}
	svc := &federationService{modRepo: modRepo}
	err := svc.setActorCursor(context.Background(), "actor1", "new-cursor")
	assert.NoError(t, err)
	_, ok := modRepo.configs["atproto_cursor_actor1"]
	assert.True(t, ok)
}

func TestGetActorNextAt_WithModRepo(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	modRepo := &mockModRepo{
		configs: map[string]*domain.InstanceConfig{
			"atproto_actor_actor1_next_at": {
				Value: mustMarshal(ts.Format(time.RFC3339)),
			},
		},
	}
	svc := &federationService{modRepo: modRepo}
	result := svc.getActorNextAt(context.Background(), "actor1")
	assert.Equal(t, ts, result)
}

func TestSetActorNextAt_WithModRepo(t *testing.T) {
	modRepo := &mockModRepo{configs: map[string]*domain.InstanceConfig{}}
	svc := &federationService{modRepo: modRepo}
	svc.setActorNextAt(context.Background(), "actor1", time.Now())
	_, ok := modRepo.configs["atproto_actor_actor1_next_at"]
	assert.True(t, ok)
}

func TestGetActorAttempts_WithModRepo(t *testing.T) {
	modRepo := &mockModRepo{
		configs: map[string]*domain.InstanceConfig{
			"atproto_actor_actor1_attempts": {Value: json.RawMessage(`3`)},
		},
	}
	svc := &federationService{modRepo: modRepo}
	assert.Equal(t, 3, svc.getActorAttempts(context.Background(), "actor1"))
}

func TestSetActorAttempts_WithModRepo(t *testing.T) {
	modRepo := &mockModRepo{configs: map[string]*domain.InstanceConfig{}}
	svc := &federationService{modRepo: modRepo}
	svc.setActorAttempts(context.Background(), "actor1", 5)
	cfg := modRepo.configs["atproto_actor_actor1_attempts"]
	require.NotNil(t, cfg)
	var n int
	_ = json.Unmarshal(cfg.Value, &n)
	assert.Equal(t, 5, n)
}

func TestBumpActorBackoff_WithModRepo(t *testing.T) {
	modRepo := &mockModRepo{configs: map[string]*domain.InstanceConfig{
		"atproto_actor_actor1_attempts": {Value: json.RawMessage(`2`)},
	}}
	svc := &federationService{modRepo: modRepo}
	svc.bumpActorBackoff(context.Background(), "actor1")

	cfg := modRepo.configs["atproto_actor_actor1_attempts"]
	require.NotNil(t, cfg)
	var n int
	_ = json.Unmarshal(cfg.Value, &n)
	assert.Equal(t, 3, n)

	_, ok := modRepo.configs["atproto_actor_actor1_next_at"]
	assert.True(t, ok)
}

func TestBumpActorBackoff_WithRepo(t *testing.T) {
	repo := &mockFederationRepo{
		actorStates: map[string]*actorState{
			"actor1": {attempts: 2},
		},
	}
	svc := &federationService{repo: repo}
	svc.bumpActorBackoff(context.Background(), "actor1")

	state := repo.actorStates["actor1"]
	assert.Equal(t, 3, state.attempts)
	assert.NotNil(t, state.nextAt)
}

func TestExtractEmbedInfo(t *testing.T) {
	svc := &federationService{}

	t.Run("no embed", func(t *testing.T) {
		rec := map[string]any{"text": "no embed"}
		et, eu, etl, ed := svc.extractEmbedInfo(rec)
		assert.Nil(t, et)
		assert.Nil(t, eu)
		assert.Nil(t, etl)
		assert.Nil(t, ed)
	})

	t.Run("images embed", func(t *testing.T) {
		rec := map[string]any{
			"embed": map[string]any{"$type": "app.bsky.embed.images"},
		}
		et, _, _, _ := svc.extractEmbedInfo(rec)
		require.NotNil(t, et)
		assert.Equal(t, "images", *et)
	})

	t.Run("video embed", func(t *testing.T) {
		rec := map[string]any{
			"embed": map[string]any{"$type": "app.bsky.embed.video"},
		}
		et, _, _, _ := svc.extractEmbedInfo(rec)
		require.NotNil(t, et)
		assert.Equal(t, "video", *et)
	})

	t.Run("record embed", func(t *testing.T) {
		rec := map[string]any{
			"embed": map[string]any{"$type": "app.bsky.embed.record"},
		}
		et, _, _, _ := svc.extractEmbedInfo(rec)
		require.NotNil(t, et)
		assert.Equal(t, "record", *et)
	})

	t.Run("external embed without details", func(t *testing.T) {
		rec := map[string]any{
			"embed": map[string]any{"$type": "app.bsky.embed.external"},
		}
		et, eu, etl, ed := svc.extractEmbedInfo(rec)
		require.NotNil(t, et)
		assert.Equal(t, "external", *et)
		assert.Nil(t, eu)
		assert.Nil(t, etl)
		assert.Nil(t, ed)
	})

	t.Run("recordWithMedia video", func(t *testing.T) {
		rec := map[string]any{
			"embed": map[string]any{
				"$type": "app.bsky.embed.recordWithMedia",
				"media": map[string]any{
					"$type": "app.bsky.embed.video",
				},
			},
		}
		et, _, _, _ := svc.extractEmbedInfo(rec)
		require.NotNil(t, et)
		assert.Equal(t, "video", *et)
	})

	t.Run("recordWithMedia images", func(t *testing.T) {
		rec := map[string]any{
			"embed": map[string]any{
				"$type": "app.bsky.embed.recordWithMedia",
				"media": map[string]any{
					"$type": "app.bsky.embed.images",
				},
			},
		}
		et, _, _, _ := svc.extractEmbedInfo(rec)
		require.NotNil(t, et)
		assert.Equal(t, "images", *et)
	})

	t.Run("recordWithMedia heuristic video key", func(t *testing.T) {
		rec := map[string]any{
			"embed": map[string]any{
				"$type": "app.bsky.embed.recordWithMedia",
				"media": map[string]any{
					"video": map[string]any{},
				},
			},
		}
		et, _, _, _ := svc.extractEmbedInfo(rec)
		require.NotNil(t, et)
		assert.Equal(t, "video", *et)
	})

	t.Run("recordWithMedia heuristic images key", func(t *testing.T) {
		rec := map[string]any{
			"embed": map[string]any{
				"$type": "app.bsky.embed.recordWithMedia",
				"media": map[string]any{
					"images": []any{},
				},
			},
		}
		et, _, _, _ := svc.extractEmbedInfo(rec)
		require.NotNil(t, et)
		assert.Equal(t, "images", *et)
	})

	t.Run("unknown embed type", func(t *testing.T) {
		rec := map[string]any{
			"embed": map[string]any{"$type": "app.bsky.embed.unknown"},
		}
		et, _, _, _ := svc.extractEmbedInfo(rec)
		assert.Nil(t, et)
	})
}

func TestGetIngestActors(t *testing.T) {
	t.Run("from repo", func(t *testing.T) {
		repo := &mockFederationRepo{actors: []string{"actor1", "actor2"}}
		svc := &federationService{repo: repo}
		result := svc.getIngestActors(context.Background())
		assert.Equal(t, []string{"actor1", "actor2"}, result)
	})

	t.Run("from modRepo config", func(t *testing.T) {
		repo := &mockFederationRepo{actors: nil}
		modRepo := &mockModRepo{
			configs: map[string]*domain.InstanceConfig{
				"atproto_ingest_actors": {Value: json.RawMessage(`["actor1","actor2"]`)},
			},
		}
		svc := &federationService{repo: repo, modRepo: modRepo}
		result := svc.getIngestActors(context.Background())
		assert.Equal(t, []string{"actor1", "actor2"}, result)
	})

	t.Run("nil modRepo", func(t *testing.T) {
		repo := &mockFederationRepo{actors: nil}
		svc := &federationService{repo: repo, modRepo: nil}
		result := svc.getIngestActors(context.Background())
		assert.Nil(t, result)
	})
}

func TestNewFederationService_WithHardening(t *testing.T) {
	repo := &mockFederationRepo{}
	modRepo := &mockModRepo{}
	atproto := &mockAtprotoPublisher{}
	mockH := new(MockHardeningRepository)
	cfg := &config.Config{}

	svc := NewFederationService(repo, modRepo, atproto, cfg, mockH)
	require.NotNil(t, svc)

	fs := svc.(*federationService)
	assert.NotNil(t, fs.circuitBreaker)
	assert.NotNil(t, fs.backpressure)
}

func TestHasBlockedLabel(t *testing.T) {
	svc := &federationService{}
	blocked := map[string]struct{}{"nsfw": {}, "spam": {}}

	t.Run("no labels", func(t *testing.T) {
		post := map[string]any{"uri": "test"}
		assert.False(t, svc.hasBlockedLabel(post, blocked))
	})

	t.Run("labels without values array", func(t *testing.T) {
		post := map[string]any{"labels": map[string]any{}}
		assert.False(t, svc.hasBlockedLabel(post, blocked))
	})

	t.Run("blocked label present", func(t *testing.T) {
		post := map[string]any{
			"labels": map[string]any{
				"values": []any{
					map[string]any{"val": "nsfw"},
				},
			},
		}
		assert.True(t, svc.hasBlockedLabel(post, blocked))
	})

	t.Run("no blocked label", func(t *testing.T) {
		post := map[string]any{
			"labels": map[string]any{
				"values": []any{
					map[string]any{"val": "safe"},
				},
			},
		}
		assert.False(t, svc.hasBlockedLabel(post, blocked))
	})

	t.Run("non-map value in values array", func(t *testing.T) {
		post := map[string]any{
			"labels": map[string]any{
				"values": []any{"not-a-map"},
			},
		}
		assert.False(t, svc.hasBlockedLabel(post, blocked))
	})
}

func TestLoadBlockedLabels(t *testing.T) {
	t.Run("nil modRepo", func(t *testing.T) {
		svc := &federationService{modRepo: nil}
		result := svc.loadBlockedLabels(context.Background())
		assert.Empty(t, result)
	})

	t.Run("with config", func(t *testing.T) {
		modRepo := &mockModRepo{
			configs: map[string]*domain.InstanceConfig{
				"atproto_block_labels": {Value: json.RawMessage(`["nsfw","spam"]`)},
			},
		}
		svc := &federationService{modRepo: modRepo}
		result := svc.loadBlockedLabels(context.Background())
		assert.Contains(t, result, "nsfw")
		assert.Contains(t, result, "spam")
	})
}

func TestIngestActor_NilAtproto(t *testing.T) {
	svc := &federationService{atproto: nil, cfg: &config.Config{}}
	err := svc.ingestActor(context.Background(), "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "atproto not configured")
}

func TestFederationHardening_MoveToDLQ(t *testing.T) {
	mockH := new(MockHardeningRepository)
	job := &domain.FederationJob{
		ID:          "job-1",
		JobType:     "publish_post",
		Attempts:    5,
		MaxAttempts: 5,
	}
	mockH.On("MoveToDLQ", context.Background(), job, "max attempts reached").Return(nil)

	err := mockH.MoveToDLQ(context.Background(), job, "max attempts reached")
	assert.NoError(t, err)
	mockH.AssertExpectations(t)
}

func mustMarshal(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
