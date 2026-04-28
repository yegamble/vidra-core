package repository

import (
	"strings"
	"testing"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestBuildAssignmentFilter_Empty(t *testing.T) {
	whereSQL, args := buildAssignmentFilter(domain.ListAssignmentsOpts{})
	require.Equal(t, "", whereSQL)
	require.Empty(t, args)
}

func TestBuildAssignmentFilter_RunnerIDOnly(t *testing.T) {
	id := uuid.New()
	whereSQL, args := buildAssignmentFilter(domain.ListAssignmentsOpts{RunnerID: &id})
	require.Equal(t, " WHERE runner_id = $1", whereSQL)
	require.Equal(t, []any{id}, args)
}

func TestBuildAssignmentFilter_StateOnly(t *testing.T) {
	whereSQL, args := buildAssignmentFilter(domain.ListAssignmentsOpts{
		State: []domain.RemoteRunnerJobState{
			domain.RemoteRunnerJobStateFailed,
			domain.RemoteRunnerJobStateAborted,
		},
	})
	require.True(t, strings.Contains(whereSQL, "state IN ("))
	require.True(t, strings.Contains(whereSQL, "$1"))
	require.True(t, strings.Contains(whereSQL, "$2"))
	require.Equal(t, []any{"failed", "aborted"}, args)
}

func TestBuildAssignmentFilter_RunnerAndState(t *testing.T) {
	id := uuid.New()
	whereSQL, args := buildAssignmentFilter(domain.ListAssignmentsOpts{
		RunnerID: &id,
		State:    []domain.RemoteRunnerJobState{domain.RemoteRunnerJobStateRunning},
	})
	// runner_id is the first clause, state IN is the second.
	require.True(t, strings.Contains(whereSQL, "runner_id = $1"))
	require.True(t, strings.Contains(whereSQL, "state IN ($2)"))
	require.True(t, strings.Contains(whereSQL, " AND "))
	require.Equal(t, []any{id, "running"}, args)
}
