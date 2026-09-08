package videoimport

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/video"
)

// The import half of A28's "a malware rejection is invisible to the creator":
// the import job settled `state: done` with no error at all, so the owner saw a
// successful import of a video that had been refused.

// TestImportMalwareRejectionDeadLettersWithTheNeutralSentence.
func TestImportMalwareRejectionDeadLettersWithTheNeutralSentence(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	pipe.processErr = &video.MalwareRejectedError{Outcome: "infected"}
	svc := NewService(repo, pipe, 1<<20, WithHTTPClient(&http.Client{}))
	origin := originServer(t)
	vid := seedVideo(pipe, uuid.New())

	if _, err := svc.Enqueue(ctx, vid, loopbackHost(origin.URL)+"/clip.mp4", ResolverDirect); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}

	j := repo.latest(vid)
	if j.State != StateFailed {
		t.Errorf("job state = %q, want %q — a refused import that reads 'done' is a lie the owner acts on",
			j.State, StateFailed)
	}
	if j.Error != video.SafetyScanRejectedMessage {
		t.Errorf("job error = %q, want the neutral sentence %q", j.Error, video.SafetyScanRejectedMessage)
	}
	for _, leak := range []string{"malware", "infected", "clam", "signature"} {
		if strings.Contains(strings.ToLower(j.Error), leak) {
			t.Errorf("job error %q leaks %q", j.Error, leak)
		}
	}
	// One attempt, not five: the verdict does not change on the ladder.
	if j.Attempts > 1 {
		t.Errorf("attempts = %d, want 1", j.Attempts)
	}
	if pipe.published[vid] {
		t.Error("the rejected import was marked published")
	}
}
