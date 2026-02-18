package torrent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPopularityPrioritizer_EmptySlice(t *testing.T) {
	p := &PopularityPrioritizer{}
	priorities := p.CalculatePriorities(nil)
	assert.Empty(t, priorities)
}

func TestPopularityPrioritizer_SingleTorrent(t *testing.T) {
	p := &PopularityPrioritizer{}
	torrents := []TorrentPriority{
		{InfoHash: "abc123", Seeders: 0, Leechers: 5, Uploaded: 0},
	}
	priorities := p.CalculatePriorities(torrents)
	require.Len(t, priorities, 1)
	assert.GreaterOrEqual(t, priorities["abc123"], 0.1)
	assert.LessOrEqual(t, priorities["abc123"], 1.0)
}

func TestPopularityPrioritizer_HighNeedHigherThanLowNeed(t *testing.T) {
	p := &PopularityPrioritizer{}
	high := TorrentPriority{InfoHash: "high", Seeders: 0, Leechers: 100, Uploaded: 0}
	low := TorrentPriority{InfoHash: "low", Seeders: 100, Leechers: 1, Uploaded: 0}
	priorities := p.CalculatePriorities([]TorrentPriority{high, low})
	require.Len(t, priorities, 2)
	assert.Greater(t, priorities["high"], priorities["low"],
		"high-need torrent should have higher priority than low-need torrent")
}

func TestPopularityPrioritizer_PriorityCappedAtOne(t *testing.T) {
	p := &PopularityPrioritizer{}
	torrents := []TorrentPriority{
		{InfoHash: "extreme", Seeders: 0, Leechers: 1_000_000, Uploaded: 100 * 1024 * 1024 * 1024},
	}
	priorities := p.CalculatePriorities(torrents)
	assert.LessOrEqual(t, priorities["extreme"], 1.0, "priority must not exceed 1.0")
}

func TestPopularityPrioritizer_MinPriorityEnforced(t *testing.T) {
	p := &PopularityPrioritizer{}
	torrents := []TorrentPriority{
		{InfoHash: "cold", Seeders: 1000, Leechers: 0, Uploaded: 0},
	}
	priorities := p.CalculatePriorities(torrents)
	assert.Equal(t, 0.1, priorities["cold"], "should use minimum priority of 0.1 when need score is too low")
}

func TestPopularityPrioritizer_AllPrioritiesInValidRange(t *testing.T) {
	p := &PopularityPrioritizer{}
	torrents := []TorrentPriority{
		{InfoHash: "t1", Seeders: 5, Leechers: 50, Uploaded: 1024 * 1024 * 1024},
		{InfoHash: "t2", Seeders: 100, Leechers: 10, Uploaded: 0},
		{InfoHash: "t3", Seeders: 1, Leechers: 1, Uploaded: 512 * 1024 * 1024},
	}
	priorities := p.CalculatePriorities(torrents)
	for hash, priority := range priorities {
		assert.GreaterOrEqual(t, priority, 0.1, "priority for %s should be >= 0.1", hash)
		assert.LessOrEqual(t, priority, 1.0, "priority for %s should be <= 1.0", hash)
	}
}

func TestPopularityPrioritizer_InfoHashPreserved(t *testing.T) {
	p := &PopularityPrioritizer{}
	torrents := []TorrentPriority{
		{InfoHash: "hash1"},
		{InfoHash: "hash2"},
	}
	priorities := p.CalculatePriorities(torrents)
	assert.Contains(t, priorities, "hash1")
	assert.Contains(t, priorities, "hash2")
}

func TestFIFOPrioritizer_EmptySlice(t *testing.T) {
	p := &FIFOPrioritizer{}
	priorities := p.CalculatePriorities(nil)
	assert.Empty(t, priorities)
}

func TestFIFOPrioritizer_AllEqualPriority(t *testing.T) {
	p := &FIFOPrioritizer{}
	torrents := []TorrentPriority{
		{InfoHash: "a"},
		{InfoHash: "b"},
		{InfoHash: "c"},
	}
	priorities := p.CalculatePriorities(torrents)
	require.Len(t, priorities, 3)
	assert.Equal(t, 0.5, priorities["a"])
	assert.Equal(t, 0.5, priorities["b"])
	assert.Equal(t, 0.5, priorities["c"])
}

func TestFIFOPrioritizer_SingleTorrent(t *testing.T) {
	p := &FIFOPrioritizer{}
	priorities := p.CalculatePriorities([]TorrentPriority{{InfoHash: "only"}})
	require.Len(t, priorities, 1)
	assert.Equal(t, 0.5, priorities["only"])
}

func TestFIFOPrioritizer_IgnoresStats(t *testing.T) {
	p := &FIFOPrioritizer{}
	torrents := []TorrentPriority{
		{InfoHash: "busy", Seeders: 1000, Leechers: 5000, Uploaded: 100 * 1024 * 1024 * 1024},
		{InfoHash: "empty", Seeders: 0, Leechers: 0, Uploaded: 0},
	}
	priorities := p.CalculatePriorities(torrents)
	assert.Equal(t, priorities["busy"], priorities["empty"], "FIFO should give equal priority regardless of stats")
}

func TestCalculateRate_ZeroBytes(t *testing.T) {
	rate := calculateRate(0, time.Now().Add(-10*time.Second))
	assert.Equal(t, 0.0, rate)
}

func TestCalculateRate_NegativeBytes(t *testing.T) {
	rate := calculateRate(-100, time.Now().Add(-10*time.Second))
	assert.Equal(t, 0.0, rate)
}

func TestCalculateRate_PositiveBytesReturnsPositiveRate(t *testing.T) {
	rate := calculateRate(1024, time.Now().Add(-2*time.Second))
	assert.Greater(t, rate, 0.0, "positive bytes with elapsed time should yield positive rate")
}

func TestCalculateRate_LargerBytesHigherRate(t *testing.T) {
	startedAt := time.Now().Add(-5 * time.Second)
	rateSmall := calculateRate(1024, startedAt)
	rateLarge := calculateRate(1024*1024, startedAt)
	assert.Greater(t, rateLarge, rateSmall, "more bytes should yield higher rate for same duration")
}
