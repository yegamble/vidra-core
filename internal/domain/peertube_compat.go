package domain

import (
	"time"

	"github.com/google/uuid"
)

type ChannelCollaboratorStatus string

const (
	ChannelCollaboratorStatusPending  ChannelCollaboratorStatus = "pending"
	ChannelCollaboratorStatusAccepted ChannelCollaboratorStatus = "accepted"
	ChannelCollaboratorStatusRejected ChannelCollaboratorStatus = "rejected"
)

type ChannelCollaborator struct {
	ID          uuid.UUID                 `json:"id" db:"id"`
	ChannelID   uuid.UUID                 `json:"channelId" db:"channel_id"`
	UserID      uuid.UUID                 `json:"userId" db:"user_id"`
	InvitedBy   uuid.UUID                 `json:"invitedBy" db:"invited_by"`
	Role        string                    `json:"role" db:"role"`
	Status      ChannelCollaboratorStatus `json:"status" db:"status"`
	RespondedAt *time.Time                `json:"respondedAt,omitempty" db:"responded_at"`
	CreatedAt   time.Time                 `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time                 `json:"updatedAt" db:"updated_at"`
	User        *User                     `json:"account,omitempty" db:"-"`
}

type RemoteRunnerStatus string

const (
	RemoteRunnerStatusRegistered   RemoteRunnerStatus = "registered"
	RemoteRunnerStatusUnregistered RemoteRunnerStatus = "unregistered"
)

type RemoteRunner struct {
	ID          uuid.UUID          `json:"id" db:"id"`
	Name        string             `json:"name" db:"name"`
	Description string             `json:"description" db:"description"`
	Token       string             `json:"token,omitempty" db:"token"`
	Status      RemoteRunnerStatus `json:"status" db:"status"`
	CreatedBy   *uuid.UUID         `json:"createdBy,omitempty" db:"created_by"`
	LastSeenAt  *time.Time         `json:"lastSeenAt,omitempty" db:"last_seen_at"`
	CreatedAt   time.Time          `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time          `json:"updatedAt" db:"updated_at"`
}

type RemoteRunnerRegistrationToken struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	Token          string     `json:"token" db:"token"`
	CreatedBy      *uuid.UUID `json:"createdBy,omitempty" db:"created_by"`
	ExpiresAt      *time.Time `json:"expiresAt,omitempty" db:"expires_at"`
	UsedAt         *time.Time `json:"usedAt,omitempty" db:"used_at"`
	UsedByRunnerID *uuid.UUID `json:"usedByRunnerId,omitempty" db:"used_by_runner_id"`
	CreatedAt      time.Time  `json:"createdAt" db:"created_at"`
}

type RemoteRunnerJobState string

const (
	RemoteRunnerJobStateAssigned  RemoteRunnerJobState = "assigned"
	RemoteRunnerJobStateAccepted  RemoteRunnerJobState = "accepted"
	RemoteRunnerJobStateRunning   RemoteRunnerJobState = "running"
	RemoteRunnerJobStateCompleted RemoteRunnerJobState = "completed"
	RemoteRunnerJobStateFailed    RemoteRunnerJobState = "failed"
	RemoteRunnerJobStateAborted   RemoteRunnerJobState = "aborted"
	RemoteRunnerJobStateCancelled RemoteRunnerJobState = "cancelled"
)

type RemoteRunnerJobAssignment struct {
	ID          uuid.UUID            `json:"id" db:"id"`
	RunnerID    uuid.UUID            `json:"runnerId" db:"runner_id"`
	EncodingJob string               `json:"jobUUID" db:"encoding_job_id"`
	State       RemoteRunnerJobState `json:"state" db:"state"`
	Progress    int                  `json:"progress" db:"progress"`
	LastError   string               `json:"lastError,omitempty" db:"last_error"`
	Metadata    map[string]any       `json:"metadata,omitempty" db:"metadata"`
	AcceptedAt  *time.Time           `json:"acceptedAt,omitempty" db:"accepted_at"`
	CompletedAt *time.Time           `json:"completedAt,omitempty" db:"completed_at"`
	CreatedAt   time.Time            `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time            `json:"updatedAt" db:"updated_at"`
	Runner      *RemoteRunner        `json:"runner,omitempty" db:"-"`
	Job         *EncodingJob         `json:"job,omitempty" db:"-"`
}
