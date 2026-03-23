package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func ptrFloat64(v float64) *float64 { return &v }

func TestStudioEditRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		req     StudioEditRequest
		wantErr bool
	}{
		{
			name:    "empty tasks",
			req:     StudioEditRequest{Tasks: []StudioTask{}},
			wantErr: true,
		},
		{
			name: "unknown task name",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "unknown", Options: StudioTaskOptions{}},
			}},
			wantErr: true,
		},
		{
			name: "valid cut task",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "cut", Options: StudioTaskOptions{Start: ptrFloat64(5), End: ptrFloat64(30)}},
			}},
			wantErr: false,
		},
		{
			name: "cut task missing start",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "cut", Options: StudioTaskOptions{End: ptrFloat64(30)}},
			}},
			wantErr: true,
		},
		{
			name: "cut task missing end",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "cut", Options: StudioTaskOptions{Start: ptrFloat64(5)}},
			}},
			wantErr: true,
		},
		{
			name: "cut task start >= end",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "cut", Options: StudioTaskOptions{Start: ptrFloat64(30), End: ptrFloat64(5)}},
			}},
			wantErr: true,
		},
		{
			name: "cut task start equals end",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "cut", Options: StudioTaskOptions{Start: ptrFloat64(10), End: ptrFloat64(10)}},
			}},
			wantErr: true,
		},
		{
			name: "cut task negative start",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "cut", Options: StudioTaskOptions{Start: ptrFloat64(-1), End: ptrFloat64(10)}},
			}},
			wantErr: true,
		},
		{
			name: "valid add-intro task",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "add-intro", Options: StudioTaskOptions{File: "/uploads/intro.mp4"}},
			}},
			wantErr: false,
		},
		{
			name: "add-intro missing file",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "add-intro", Options: StudioTaskOptions{}},
			}},
			wantErr: true,
		},
		{
			name: "valid add-outro task",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "add-outro", Options: StudioTaskOptions{File: "/uploads/outro.mp4"}},
			}},
			wantErr: false,
		},
		{
			name: "add-outro missing file",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "add-outro", Options: StudioTaskOptions{}},
			}},
			wantErr: true,
		},
		{
			name: "valid add-watermark task",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "add-watermark", Options: StudioTaskOptions{File: "/uploads/logo.png"}},
			}},
			wantErr: false,
		},
		{
			name: "add-watermark missing file",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "add-watermark", Options: StudioTaskOptions{}},
			}},
			wantErr: true,
		},
		{
			name: "multiple valid tasks",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "cut", Options: StudioTaskOptions{Start: ptrFloat64(0), End: ptrFloat64(60)}},
				{Name: "add-watermark", Options: StudioTaskOptions{File: "/uploads/logo.png"}},
			}},
			wantErr: false,
		},
		{
			name: "one valid one invalid",
			req: StudioEditRequest{Tasks: []StudioTask{
				{Name: "cut", Options: StudioTaskOptions{Start: ptrFloat64(0), End: ptrFloat64(60)}},
				{Name: "add-intro", Options: StudioTaskOptions{}},
			}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidStudioTask)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidStudioTaskNames(t *testing.T) {
	expected := []string{"cut", "add-intro", "add-outro", "add-watermark"}
	for _, name := range expected {
		assert.True(t, ValidStudioTaskNames[name], "expected %q to be a valid task name", name)
	}
	assert.False(t, ValidStudioTaskNames["resize"], "resize should not be a valid task name")
}

func TestStudioJobStatus_Constants(t *testing.T) {
	assert.Equal(t, StudioJobStatus("pending"), StudioJobStatusPending)
	assert.Equal(t, StudioJobStatus("processing"), StudioJobStatusProcessing)
	assert.Equal(t, StudioJobStatus("completed"), StudioJobStatusCompleted)
	assert.Equal(t, StudioJobStatus("failed"), StudioJobStatusFailed)
}
