package plugin

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHookManager_Register(t *testing.T) {
	hm := NewHookManager()

	called := false
	hookFunc := func(ctx context.Context, data any) error {
		called = true
		return nil
	}

	hm.Register(EventVideoUploaded, "test-plugin", hookFunc)

	// Check if hook was registered
	plugins := hm.GetRegisteredHooks(EventVideoUploaded)
	if len(plugins) != 1 {
		t.Errorf("Expected 1 plugin, got %d", len(plugins))
	}

	if plugins[0] != "test-plugin" {
		t.Errorf("Expected plugin name 'test-plugin', got '%s'", plugins[0])
	}

	// Trigger the hook
	err := hm.Trigger(context.Background(), EventVideoUploaded, nil)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if !called {
		t.Error("Expected hook to be called")
	}
}

func TestHookManager_Unregister(t *testing.T) {
	hm := NewHookManager()

	hookFunc := func(ctx context.Context, data any) error {
		return nil
	}

	hm.Register(EventVideoUploaded, "test-plugin", hookFunc)
	hm.Unregister(EventVideoUploaded, "test-plugin")

	plugins := hm.GetRegisteredHooks(EventVideoUploaded)
	if len(plugins) != 0 {
		t.Errorf("Expected 0 plugins after unregister, got %d", len(plugins))
	}
}

func TestHookManager_UnregisterPluginHooks(t *testing.T) {
	hm := NewHookManager()

	hookFunc := func(ctx context.Context, data any) error {
		return nil
	}

	hm.Register(EventVideoUploaded, "test-plugin", hookFunc)
	hm.Register(EventVideoProcessed, "test-plugin", hookFunc)
	hm.Register(EventVideoDeleted, "test-plugin", hookFunc)

	hm.UnregisterPluginHooks("test-plugin")

	// Check all event types
	if len(hm.GetRegisteredHooks(EventVideoUploaded)) != 0 {
		t.Error("Expected no hooks for EventVideoUploaded")
	}
	if len(hm.GetRegisteredHooks(EventVideoProcessed)) != 0 {
		t.Error("Expected no hooks for EventVideoProcessed")
	}
	if len(hm.GetRegisteredHooks(EventVideoDeleted)) != 0 {
		t.Error("Expected no hooks for EventVideoDeleted")
	}
}

func TestHookManager_TriggerMultipleHooks(t *testing.T) {
	hm := NewHookManager()

	callOrder := []string{}

	hook1 := func(ctx context.Context, data any) error {
		callOrder = append(callOrder, "plugin1")
		return nil
	}

	hook2 := func(ctx context.Context, data any) error {
		callOrder = append(callOrder, "plugin2")
		return nil
	}

	hm.Register(EventVideoUploaded, "plugin1", hook1)
	hm.Register(EventVideoUploaded, "plugin2", hook2)

	err := hm.Trigger(context.Background(), EventVideoUploaded, nil)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if len(callOrder) != 2 {
		t.Errorf("Expected 2 hooks to be called, got %d", len(callOrder))
	}
}

func TestHookManager_TriggerWithError(t *testing.T) {
	hm := NewHookManager()
	hm.SetFailureMode(FailureModeContinue)

	expectedErr := errors.New("test error")

	hook1 := func(ctx context.Context, data any) error {
		return expectedErr
	}

	hook2 := func(ctx context.Context, data any) error {
		return nil
	}

	hm.Register(EventVideoUploaded, "plugin1", hook1)
	hm.Register(EventVideoUploaded, "plugin2", hook2)

	err := hm.Trigger(context.Background(), EventVideoUploaded, nil)
	if err == nil {
		t.Error("Expected error, got nil")
	}
}

func TestHookManager_FailureModeStop(t *testing.T) {
	hm := NewHookManager()
	hm.SetFailureMode(FailureModeStop)

	expectedErr := errors.New("test error")

	hook1 := func(ctx context.Context, data any) error {
		return expectedErr
	}

	hook2 := func(ctx context.Context, data any) error {
		return nil
	}

	hm.Register(EventVideoUploaded, "plugin1", hook1)
	hm.Register(EventVideoUploaded, "plugin2", hook2)

	err := hm.Trigger(context.Background(), EventVideoUploaded, nil)
	if err == nil {
		t.Error("Expected error, got nil")
	}

	// Second hook should not be called in FailureModeStop
	// Note: This is not guaranteed due to map iteration order
	// but the error should be returned
}

func TestHookManager_FailureModeIgnore(t *testing.T) {
	hm := NewHookManager()
	hm.SetFailureMode(FailureModeIgnore)

	expectedErr := errors.New("test error")

	hook1 := func(ctx context.Context, data any) error {
		return expectedErr
	}

	hm.Register(EventVideoUploaded, "plugin1", hook1)

	err := hm.Trigger(context.Background(), EventVideoUploaded, nil)
	if err != nil {
		t.Errorf("Expected no error in ignore mode, got %v", err)
	}
}

func TestHookManager_TriggerTimeout(t *testing.T) {
	hm := NewHookManager()
	hm.SetHookTimeout(100 * time.Millisecond)

	hook1 := func(ctx context.Context, data any) error {
		// Sleep longer than timeout
		time.Sleep(200 * time.Millisecond)
		return nil
	}

	hm.Register(EventVideoUploaded, "plugin1", hook1)

	err := hm.Trigger(context.Background(), EventVideoUploaded, nil)
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

func TestHookManager_GetAllEventTypes(t *testing.T) {
	hm := NewHookManager()

	hookFunc := func(ctx context.Context, data any) error {
		return nil
	}

	hm.Register(EventVideoUploaded, "plugin1", hookFunc)
	hm.Register(EventVideoProcessed, "plugin1", hookFunc)
	hm.Register(EventUserRegistered, "plugin2", hookFunc)

	eventTypes := hm.GetAllEventTypes()
	if len(eventTypes) != 3 {
		t.Errorf("Expected 3 event types, got %d", len(eventTypes))
	}
}

func TestHookManager_Count(t *testing.T) {
	hm := NewHookManager()

	hookFunc := func(ctx context.Context, data any) error {
		return nil
	}

	hm.Register(EventVideoUploaded, "plugin1", hookFunc)
	hm.Register(EventVideoProcessed, "plugin1", hookFunc)
	hm.Register(EventVideoUploaded, "plugin2", hookFunc)

	count := hm.Count()
	if count != 3 {
		t.Errorf("Expected 3 hooks, got %d", count)
	}
}

func TestHookManager_Clear(t *testing.T) {
	hm := NewHookManager()

	hookFunc := func(ctx context.Context, data any) error {
		return nil
	}

	hm.Register(EventVideoUploaded, "plugin1", hookFunc)
	hm.Register(EventVideoProcessed, "plugin1", hookFunc)

	hm.Clear()

	count := hm.Count()
	if count != 0 {
		t.Errorf("Expected 0 hooks after clear, got %d", count)
	}
}

func TestHookManager_TriggerAsync(t *testing.T) {
	hm := NewHookManager()

	called := false
	hookFunc := func(ctx context.Context, data any) error {
		called = true
		return nil
	}

	hm.Register(EventVideoUploaded, "test-plugin", hookFunc)

	// Trigger async and wait for completion
	errChan := hm.TriggerAsync(context.Background(), EventVideoUploaded, nil)

	// Wait for channel to close or receive error
	select {
	case err, ok := <-errChan:
		if ok && err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for async hook")
	}

	if !called {
		t.Error("Expected hook to be called")
	}
}

func TestHookManager_EventData(t *testing.T) {
	hm := NewHookManager()

	var receivedType EventType
	var receivedData any
	hookFunc := func(ctx context.Context, data any) error {
		if ed, ok := data.(*EventData); ok {
			receivedType = ed.Type
			receivedData = ed.Data
		}
		return nil
	}

	hm.Register(EventVideoUploaded, "test-plugin", hookFunc)

	testData := "test-data"
	err := hm.Trigger(context.Background(), EventVideoUploaded, testData)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if receivedType != EventVideoUploaded {
		t.Errorf("Expected event type %s, got %s", EventVideoUploaded, receivedType)
	}

	if receivedData != testData {
		t.Errorf("Expected data %v, got %v", testData, receivedData)
	}
}
