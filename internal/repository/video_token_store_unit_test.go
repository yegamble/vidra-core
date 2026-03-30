package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMiniRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func TestRedisVideoTokenStore_Set(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mr, rdb := setupMiniRedis(t)
		store := NewRedisVideoTokenStore(rdb)

		err := store.Set(ctx, "tok:abc", "value1", time.Minute)
		require.NoError(t, err)

		got, err := mr.Get("tok:abc")
		require.NoError(t, err)
		assert.Equal(t, "value1", got)
	})

	t.Run("redis error", func(t *testing.T) {
		mr, rdb := setupMiniRedis(t)
		store := NewRedisVideoTokenStore(rdb)

		mr.Close() // force connection error

		err := store.Set(ctx, "tok:err", "val", time.Minute)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "video token store set")
	})
}

func TestRedisVideoTokenStore_Get(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mr, rdb := setupMiniRedis(t)
		store := NewRedisVideoTokenStore(rdb)

		require.NoError(t, mr.Set("tok:get1", "my-token"))

		val, err := store.Get(ctx, "tok:get1")
		require.NoError(t, err)
		assert.Equal(t, "my-token", val)
	})

	t.Run("key not found", func(t *testing.T) {
		_, rdb := setupMiniRedis(t)
		store := NewRedisVideoTokenStore(rdb)

		val, err := store.Get(ctx, "tok:missing")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "video token store get")
		assert.Empty(t, val)
	})

	t.Run("redis error", func(t *testing.T) {
		mr, rdb := setupMiniRedis(t)
		store := NewRedisVideoTokenStore(rdb)

		mr.Close()

		val, err := store.Get(ctx, "tok:any")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "video token store get")
		assert.Empty(t, val)
	})
}

func TestRedisVideoTokenStore_Del(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		mr, rdb := setupMiniRedis(t)
		store := NewRedisVideoTokenStore(rdb)

		require.NoError(t, mr.Set("tok:del1", "v"))

		err := store.Del(ctx, "tok:del1")
		require.NoError(t, err)
		assert.False(t, mr.Exists("tok:del1"))
	})

	t.Run("delete non-existent key is not an error", func(t *testing.T) {
		_, rdb := setupMiniRedis(t)
		store := NewRedisVideoTokenStore(rdb)

		err := store.Del(ctx, "tok:nonexistent")
		require.NoError(t, err)
	})

	t.Run("redis error", func(t *testing.T) {
		mr, rdb := setupMiniRedis(t)
		store := NewRedisVideoTokenStore(rdb)

		mr.Close()

		err := store.Del(ctx, "tok:any")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "video token store del")
	})
}
