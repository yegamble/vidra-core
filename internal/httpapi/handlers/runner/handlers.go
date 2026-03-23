package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/port"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type runnerRepository interface {
	ListRunners(ctx context.Context) ([]*domain.RemoteRunner, error)
	GetRunner(ctx context.Context, runnerID uuid.UUID) (*domain.RemoteRunner, error)
	GetRunnerByToken(ctx context.Context, token string) (*domain.RemoteRunner, error)
	TouchRunner(ctx context.Context, runnerID uuid.UUID) error
	CreateRegistrationToken(ctx context.Context, createdBy *uuid.UUID, expiresAt *time.Time) (*domain.RemoteRunnerRegistrationToken, error)
	ListRegistrationTokens(ctx context.Context) ([]*domain.RemoteRunnerRegistrationToken, error)
	DeleteRegistrationToken(ctx context.Context, tokenID uuid.UUID) error
	RegisterRunner(ctx context.Context, registrationToken, name, description string) (*domain.RemoteRunner, error)
	UnregisterRunnerByToken(ctx context.Context, token string) error
	DeleteRunner(ctx context.Context, runnerID uuid.UUID) error
	CreateAssignment(ctx context.Context, runnerID uuid.UUID, encodingJobID string) (*domain.RemoteRunnerJobAssignment, error)
	GetAssignmentByJob(ctx context.Context, jobID string) (*domain.RemoteRunnerJobAssignment, error)
	GetAssignmentForRunnerAndJob(ctx context.Context, runnerID uuid.UUID, jobID string) (*domain.RemoteRunnerJobAssignment, error)
	ListAssignments(ctx context.Context) ([]*domain.RemoteRunnerJobAssignment, error)
	UpdateAssignment(ctx context.Context, assignment *domain.RemoteRunnerJobAssignment) error
	RecordFileReceipt(ctx context.Context, assignment *domain.RemoteRunnerJobAssignment, fileKey string, details map[string]any) error
	DeleteAssignment(ctx context.Context, jobID string) error
}

type Handlers struct {
	runnerRepo   runnerRepository
	encodingRepo port.EncodingRepository
}

func NewHandlers(runnerRepo runnerRepository, encodingRepo port.EncodingRepository) *Handlers {
	return &Handlers{runnerRepo: runnerRepo, encodingRepo: encodingRepo}
}

func (h *Handlers) ListRunners(w http.ResponseWriter, r *http.Request) {
	runners, err := h.runnerRepo.ListRunners(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("list runners: %w", err))
		return
	}

	for _, runner := range runners {
		runner.Token = ""
	}

	shared.WriteJSON(w, http.StatusOK, map[string]any{"total": len(runners), "data": runners})
}

func (h *Handlers) ListRegistrationTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.runnerRepo.ListRegistrationTokens(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("list registration tokens: %w", err))
		return
	}
	if tokens == nil {
		tokens = []*domain.RemoteRunnerRegistrationToken{}
	}
	shared.WriteJSON(w, http.StatusOK, map[string]any{"total": len(tokens), "data": tokens})
}

func (h *Handlers) CreateRegistrationToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ExpiresAt *time.Time `json:"expiresAt"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	var createdBy *uuid.UUID
	if userID, ok := middleware.GetUserIDFromContext(r.Context()); ok {
		createdBy = &userID
	}

	token, err := h.runnerRepo.CreateRegistrationToken(r.Context(), createdBy, req.ExpiresAt)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("create registration token: %w", err))
		return
	}
	shared.WriteJSON(w, http.StatusCreated, token)
}

func (h *Handlers) DeleteRegistrationToken(w http.ResponseWriter, r *http.Request) {
	tokenID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_ID", "Invalid registration token ID"))
		return
	}

	if err := h.runnerRepo.DeleteRegistrationToken(r.Context(), tokenID); err != nil {
		status := http.StatusInternalServerError
		if err == domain.ErrNotFound {
			status = http.StatusNotFound
		}
		shared.WriteError(w, status, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) RegisterRunner(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RegistrationToken string `json:"registrationToken"`
		Token             string `json:"token"`
		Name              string `json:"name"`
		Description       string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Invalid request body"))
		return
	}

	token := req.RegistrationToken
	if token == "" {
		token = req.Token
	}
	if token == "" || req.Name == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "registrationToken and name are required"))
		return
	}

	runner, err := h.runnerRepo.RegisterRunner(r.Context(), token, req.Name, req.Description)
	if err != nil {
		status := http.StatusInternalServerError
		switch err {
		case domain.ErrNotFound:
			status = http.StatusNotFound
		case domain.ErrConflict:
			status = http.StatusConflict
		case domain.ErrForbidden:
			status = http.StatusForbidden
		}
		shared.WriteError(w, status, err)
		return
	}

	shared.WriteJSON(w, http.StatusCreated, map[string]any{
		"id":          runner.ID,
		"name":        runner.Name,
		"description": runner.Description,
		"runnerToken": runner.Token,
		"status":      runner.Status,
	})
}

func (h *Handlers) UnregisterRunner(w http.ResponseWriter, r *http.Request) {
	token, err := runnerTokenFromRequest(r)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, err)
		return
	}

	if err := h.runnerRepo.UnregisterRunnerByToken(r.Context(), token); err != nil {
		status := http.StatusInternalServerError
		if err == domain.ErrNotFound {
			status = http.StatusNotFound
		}
		shared.WriteError(w, status, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) DeleteRunner(w http.ResponseWriter, r *http.Request) {
	runnerID, err := uuid.Parse(chi.URLParam(r, "runnerId"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_ID", "Invalid runner ID"))
		return
	}

	if err := h.runnerRepo.DeleteRunner(r.Context(), runnerID); err != nil {
		status := http.StatusInternalServerError
		if err == domain.ErrNotFound {
			status = http.StatusNotFound
		}
		shared.WriteError(w, status, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) ListJobs(w http.ResponseWriter, r *http.Request) {
	assignments, err := h.runnerRepo.ListAssignments(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("list runner jobs: %w", err))
		return
	}

	for _, assignment := range assignments {
		if assignment.Runner == nil {
			if runner, err := h.runnerRepo.GetRunner(r.Context(), assignment.RunnerID); err == nil {
				runner.Token = ""
				assignment.Runner = runner
			}
		}
		if assignment.Job == nil {
			if job, err := h.encodingRepo.GetJob(r.Context(), assignment.EncodingJob); err == nil {
				assignment.Job = job
			}
		}
	}

	shared.WriteJSON(w, http.StatusOK, map[string]any{"total": len(assignments), "data": assignments})
}

func (h *Handlers) CancelJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobUUID")
	assignment, err := h.runnerRepo.GetAssignmentByJob(r.Context(), jobID)
	if err != nil {
		status := http.StatusInternalServerError
		if err == domain.ErrNotFound {
			status = http.StatusNotFound
		}
		shared.WriteError(w, status, err)
		return
	}

	assignment.State = domain.RemoteRunnerJobStateCancelled
	assignment.LastError = "cancelled by admin"
	if err := h.runnerRepo.UpdateAssignment(r.Context(), assignment); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("cancel runner job assignment: %w", err))
		return
	}
	if err := h.encodingRepo.SetJobError(r.Context(), jobID, assignment.LastError); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("cancel encoding job: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) DeleteJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobUUID")
	assignment, err := h.runnerRepo.GetAssignmentByJob(r.Context(), jobID)
	if err != nil && err != domain.ErrNotFound {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("load runner job assignment: %w", err))
		return
	}
	if assignment != nil {
		if job, getErr := h.encodingRepo.GetJob(r.Context(), jobID); getErr == nil && job.Status == domain.EncodingStatusProcessing {
			_ = h.encodingRepo.UpdateJobStatus(r.Context(), jobID, domain.EncodingStatusPending)
		}
	}
	if err := h.runnerRepo.DeleteAssignment(r.Context(), jobID); err != nil {
		status := http.StatusInternalServerError
		if err == domain.ErrNotFound {
			status = http.StatusNotFound
		}
		shared.WriteError(w, status, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) RequestJob(w http.ResponseWriter, r *http.Request) {
	runner, err := h.authenticateRunner(r)
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, err)
		return
	}

	job, err := h.encodingRepo.GetNextJob(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("request next encoding job: %w", err))
		return
	}
	if job == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	assignment, err := h.runnerRepo.CreateAssignment(r.Context(), runner.ID, job.ID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("create runner assignment: %w", err))
		return
	}
	assignment.Job = job

	shared.WriteJSON(w, http.StatusOK, assignment)
}

func (h *Handlers) AcceptJob(w http.ResponseWriter, r *http.Request) {
	h.updateJobState(w, r, domain.RemoteRunnerJobStateAccepted, true, false)
}

func (h *Handlers) AbortJob(w http.ResponseWriter, r *http.Request) {
	runner, assignment, err := h.loadRunnerAssignment(r)
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, err)
		return
	}
	_ = runner

	assignment.State = domain.RemoteRunnerJobStateAborted
	assignment.LastError = "aborted by runner"
	if err := h.runnerRepo.UpdateAssignment(r.Context(), assignment); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("abort runner job: %w", err))
		return
	}
	if err := h.encodingRepo.UpdateJobStatus(r.Context(), assignment.EncodingJob, domain.EncodingStatusPending); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("requeue encoding job: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) UpdateJob(w http.ResponseWriter, r *http.Request) {
	runner, assignment, err := h.loadRunnerAssignment(r)
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, err)
		return
	}
	_ = runner

	var req struct {
		Progress int            `json:"progress"`
		State    string         `json:"state"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Invalid request body"))
		return
	}

	if req.Progress > 0 {
		assignment.Progress = req.Progress
		if err := h.encodingRepo.UpdateJobProgress(r.Context(), assignment.EncodingJob, req.Progress); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("update encoding job progress: %w", err))
			return
		}
	}
	if req.State != "" {
		assignment.State = domain.RemoteRunnerJobState(req.State)
	} else {
		assignment.State = domain.RemoteRunnerJobStateRunning
	}
	if req.Metadata != nil {
		assignment.Metadata = req.Metadata
	}

	if err := h.runnerRepo.UpdateAssignment(r.Context(), assignment); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("update runner assignment: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, assignment)
}

func (h *Handlers) ErrorJob(w http.ResponseWriter, r *http.Request) {
	runner, assignment, err := h.loadRunnerAssignment(r)
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, err)
		return
	}
	_ = runner

	var req struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Invalid request body"))
		return
	}
	if req.Error == "" {
		req.Error = "runner reported an error"
	}

	assignment.State = domain.RemoteRunnerJobStateFailed
	assignment.LastError = req.Error
	if err := h.runnerRepo.UpdateAssignment(r.Context(), assignment); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("update runner assignment error: %w", err))
		return
	}
	if err := h.encodingRepo.SetJobError(r.Context(), assignment.EncodingJob, req.Error); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("update encoding job error: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) SuccessJob(w http.ResponseWriter, r *http.Request) {
	runner, assignment, err := h.loadRunnerAssignment(r)
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, err)
		return
	}
	_ = runner

	var req struct {
		Metadata map[string]any `json:"metadata"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	now := time.Now().UTC()
	assignment.State = domain.RemoteRunnerJobStateCompleted
	assignment.Progress = 100
	assignment.CompletedAt = &now
	if req.Metadata != nil {
		assignment.Metadata = req.Metadata
	}
	if err := h.runnerRepo.UpdateAssignment(r.Context(), assignment); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("complete runner assignment: %w", err))
		return
	}
	if err := h.encodingRepo.UpdateJobProgress(r.Context(), assignment.EncodingJob, 100); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("complete encoding job progress: %w", err))
		return
	}
	if err := h.encodingRepo.UpdateJobStatus(r.Context(), assignment.EncodingJob, domain.EncodingStatusCompleted); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("complete encoding job status: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) UploadJobFile(w http.ResponseWriter, r *http.Request) {
	runner, assignment, err := h.loadRunnerAssignment(r)
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, err)
		return
	}
	_ = runner

	size, readErr := io.Copy(io.Discard, r.Body)
	if readErr != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("read uploaded file payload: %w", readErr))
		return
	}

	fileKey := r.URL.Path
	details := map[string]any{
		"contentLength": size,
		"receivedAt":    time.Now().UTC(),
		"contentType":   r.Header.Get("Content-Type"),
	}
	if err := h.runnerRepo.RecordFileReceipt(r.Context(), assignment, fileKey, details); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("record runner file receipt: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) updateJobState(w http.ResponseWriter, r *http.Request, state domain.RemoteRunnerJobState, accepted, completed bool) {
	_, assignment, err := h.loadRunnerAssignment(r)
	if err != nil {
		shared.WriteError(w, http.StatusUnauthorized, err)
		return
	}

	now := time.Now().UTC()
	assignment.State = state
	if accepted {
		assignment.AcceptedAt = &now
	}
	if completed {
		assignment.CompletedAt = &now
	}
	if err := h.runnerRepo.UpdateAssignment(r.Context(), assignment); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("update runner job state: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) authenticateRunner(r *http.Request) (*domain.RemoteRunner, error) {
	token, err := runnerTokenFromRequest(r)
	if err != nil {
		return nil, err
	}

	runner, err := h.runnerRepo.GetRunnerByToken(r.Context(), token)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.ErrUnauthorized
		}
		return nil, err
	}
	if err := h.runnerRepo.TouchRunner(r.Context(), runner.ID); err != nil {
		return nil, err
	}
	runner.Token = ""
	return runner, nil
}

func (h *Handlers) loadRunnerAssignment(r *http.Request) (*domain.RemoteRunner, *domain.RemoteRunnerJobAssignment, error) {
	runner, err := h.authenticateRunner(r)
	if err != nil {
		return nil, nil, err
	}

	assignment, err := h.runnerRepo.GetAssignmentForRunnerAndJob(r.Context(), runner.ID, chi.URLParam(r, "jobUUID"))
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, nil, domain.ErrForbidden
		}
		return nil, nil, err
	}
	return runner, assignment, nil
}

func runnerTokenFromRequest(r *http.Request) (string, error) {
	if token := r.Header.Get("X-Runner-Token"); token != "" {
		return token, nil
	}

	var req struct {
		RunnerToken string `json:"runnerToken"`
		Token       string `json:"token"`
	}
	if r.Body == nil {
		return "", domain.NewDomainError("INVALID_REQUEST", "runner token is required")
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", domain.NewDomainError("INVALID_REQUEST", "runner token is required")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(body) > 0 {
		_ = json.Unmarshal(body, &req)
	}
	if req.RunnerToken != "" {
		return req.RunnerToken, nil
	}
	if req.Token != "" {
		return req.Token, nil
	}
	return "", domain.NewDomainError("INVALID_REQUEST", "runner token is required")
}
