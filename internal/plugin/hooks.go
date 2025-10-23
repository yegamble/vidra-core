package plugin

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// HookManager manages hook registrations and triggers
type HookManager struct {
	// hooks stores registered hook functions by event type and plugin name
	// map[EventType]map[pluginName]HookFunc
	hooks map[EventType]map[string]HookFunc

	// mu protects concurrent access to hooks
	mu sync.RWMutex

	// hookTimeout is the maximum time a hook can run
	hookTimeout time.Duration

	// failureMode determines how to handle hook failures
	failureMode HookFailureMode
}

// HookFailureMode determines how to handle hook failures
type HookFailureMode int

const (
	// FailureModeContinue continues executing other hooks on failure
	FailureModeContinue HookFailureMode = iota

	// FailureModeStop stops executing remaining hooks on first failure
	FailureModeStop

	// FailureModeIgnore ignores all hook failures and never returns errors
	FailureModeIgnore
)

// NewHookManager creates a new hook manager
func NewHookManager() *HookManager {
	return &HookManager{
		hooks:       make(map[EventType]map[string]HookFunc),
		hookTimeout: 30 * time.Second,
		failureMode: FailureModeContinue,
	}
}

// SetHookTimeout sets the timeout for hook execution
func (hm *HookManager) SetHookTimeout(timeout time.Duration) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.hookTimeout = timeout
}

// SetFailureMode sets the failure handling mode
func (hm *HookManager) SetFailureMode(mode HookFailureMode) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.failureMode = mode
}

// Register registers a hook function for an event type
func (hm *HookManager) Register(eventType EventType, pluginName string, hookFunc HookFunc) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.hooks[eventType] == nil {
		hm.hooks[eventType] = make(map[string]HookFunc)
	}

	hm.hooks[eventType][pluginName] = hookFunc
}

// Unregister unregisters a hook function
func (hm *HookManager) Unregister(eventType EventType, pluginName string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.hooks[eventType] != nil {
		delete(hm.hooks[eventType], pluginName)

		// Clean up empty event type map
		if len(hm.hooks[eventType]) == 0 {
			delete(hm.hooks, eventType)
		}
	}
}

// UnregisterPluginHooks unregisters all hooks for a plugin
func (hm *HookManager) UnregisterPluginHooks(pluginName string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	for eventType := range hm.hooks {
		delete(hm.hooks[eventType], pluginName)

		// Clean up empty event type map
		if len(hm.hooks[eventType]) == 0 {
			delete(hm.hooks, eventType)
		}
	}
}

// Trigger triggers all hooks registered for an event type
func (hm *HookManager) Trigger(ctx context.Context, eventType EventType, data any) error {
	hm.mu.RLock()
	hooks := hm.hooks[eventType]
	hookTimeout := hm.hookTimeout
	failureMode := hm.failureMode
	hm.mu.RUnlock()

	if len(hooks) == 0 {
		return nil // No hooks registered for this event
	}

	// Prepare event data
	eventData := &EventData{
		Type:     eventType,
		Data:     data,
		Metadata: make(map[string]any),
	}

	// Execute hooks
	var errors []error

	for pluginName, hookFunc := range hooks {
		// Create context with timeout
		hookCtx, cancel := context.WithTimeout(ctx, hookTimeout)

		// Execute hook in goroutine with timeout
		errChan := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					errChan <- fmt.Errorf("hook panic in plugin %s: %v", pluginName, r)
				}
				cancel()
			}()

			errChan <- hookFunc(hookCtx, eventData)
		}()

		// Wait for hook completion or timeout
		select {
		case err := <-errChan:
			cancel()
			if err != nil {
				hookErr := fmt.Errorf("hook error in plugin %s: %w", pluginName, err)
				errors = append(errors, hookErr)

				// Handle failure mode
				if failureMode == FailureModeStop {
					return hookErr
				}
			}

		case <-hookCtx.Done():
			cancel()
			timeoutErr := fmt.Errorf("hook timeout in plugin %s: %w", pluginName, hookCtx.Err())
			errors = append(errors, timeoutErr)

			// Handle failure mode
			if failureMode == FailureModeStop {
				return timeoutErr
			}
		}
	}

	// Return errors based on failure mode
	if failureMode == FailureModeIgnore {
		return nil
	}

	if len(errors) > 0 {
		return fmt.Errorf("hook execution errors: %v", errors)
	}

	return nil
}

// TriggerAsync triggers all hooks asynchronously without waiting for completion
// Returns a channel that will be closed when all hooks complete
func (hm *HookManager) TriggerAsync(ctx context.Context, eventType EventType, data any) <-chan error {
	errChan := make(chan error, 1)

	go func() {
		defer close(errChan)
		if err := hm.Trigger(ctx, eventType, data); err != nil {
			errChan <- err
		}
	}()

	return errChan
}

// GetRegisteredHooks returns all registered hooks for an event type
func (hm *HookManager) GetRegisteredHooks(eventType EventType) []string {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	hooks := hm.hooks[eventType]
	if hooks == nil {
		return []string{}
	}

	pluginNames := make([]string, 0, len(hooks))
	for name := range hooks {
		pluginNames = append(pluginNames, name)
	}

	return pluginNames
}

// GetAllEventTypes returns all event types that have registered hooks
func (hm *HookManager) GetAllEventTypes() []EventType {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	eventTypes := make([]EventType, 0, len(hm.hooks))
	for eventType := range hm.hooks {
		eventTypes = append(eventTypes, eventType)
	}

	return eventTypes
}

// Count returns the total number of registered hooks
func (hm *HookManager) Count() int {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	count := 0
	for _, hooks := range hm.hooks {
		count += len(hooks)
	}

	return count
}

// Clear removes all registered hooks
func (hm *HookManager) Clear() {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	hm.hooks = make(map[EventType]map[string]HookFunc)
}

// HookMiddleware is a middleware wrapper for HTTP handlers that triggers hooks
type HookMiddleware struct {
	manager *HookManager
}

// NewHookMiddleware creates a new hook middleware
func NewHookMiddleware(manager *HookManager) *HookMiddleware {
	return &HookMiddleware{
		manager: manager,
	}
}

// TriggerBefore triggers a hook before the request is processed
func (hm *HookMiddleware) TriggerBefore(eventType EventType) func(next func()) {
	return func(next func()) {
		ctx := context.Background()
		// Trigger hook asynchronously to not block request
		go func() {
			_ = hm.manager.Trigger(ctx, eventType, nil)
		}()
		next()
	}
}

// TriggerAfter triggers a hook after the request is processed
func (hm *HookMiddleware) TriggerAfter(eventType EventType, data any) {
	ctx := context.Background()
	// Trigger hook asynchronously to not block response
	go func() {
		_ = hm.manager.Trigger(ctx, eventType, data)
	}()
}
