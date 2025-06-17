package health

import (
	"testing"
)

func TestHealthStateManager(t *testing.T) {
	hsm := NewHealthStateManager()

	// Test initial state
	state := hsm.GetState()
	if state.ControllerReady != 0 {
		t.Errorf("Expected ControllerReady to be 0, got %d", state.ControllerReady)
	}
	if state.DegradedReason != "" {
		t.Errorf("Expected DegradedReason to be empty, got %s", state.DegradedReason)
	}
	if state.FatalError != "" {
		t.Errorf("Expected FatalError to be empty, got %s", state.FatalError)
	}

	// Test SetControllerReady
	hsm.SetControllerReady()
	state = hsm.GetState()
	if state.ControllerReady != 1 {
		t.Errorf("Expected ControllerReady to be 1, got %d", state.ControllerReady)
	}

	// Test SetDegraded
	hsm.SetDegraded("missing CRD")
	state = hsm.GetState()
	if state.DegradedReason != "missing CRD" {
		t.Errorf("Expected DegradedReason to be 'missing CRD', got %s", state.DegradedReason)
	}
	if state.ControllerReady != 1 {
		t.Errorf("Expected ControllerReady to remain 1, got %d", state.ControllerReady)
	}

	// Test ClearDegraded
	hsm.ClearDegraded()
	state = hsm.GetState()
	if state.DegradedReason != "" {
		t.Errorf("Expected DegradedReason to be empty after ClearDegraded, got %s", state.DegradedReason)
	}
	if state.ControllerReady != 1 {
		t.Errorf("Expected ControllerReady to remain 1, got %d", state.ControllerReady)
	}

	// Test SetFatalError
	hsm.SetFatalError("panic occurred")
	state = hsm.GetState()
	if state.FatalError != "panic occurred" {
		t.Errorf("Expected FatalError to be 'panic occurred', got %s", state.FatalError)
	}
	if state.DegradedReason != "" {
		t.Errorf("Expected DegradedReason to remain empty, got %s", state.DegradedReason)
	}
	if state.ControllerReady != 1 {
		t.Errorf("Expected ControllerReady to remain 1, got %d", state.ControllerReady)
	}
}

func TestHealthStateManagerConcurrency(t *testing.T) {
	hsm := NewHealthStateManager()

	// Test concurrent access
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			switch id % 4 {
			case 0:
				hsm.SetControllerReady()
			case 1:
				hsm.SetDegraded("test degraded")
			case 2:
				hsm.ClearDegraded()
			case 3:
				hsm.SetFatalError("test fatal")
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify final state is consistent
	state := hsm.GetState()
	if state.ControllerReady != 0 && state.ControllerReady != 1 {
		t.Errorf("Expected ControllerReady to be 0 or 1, got %d", state.ControllerReady)
	}
}

func TestGlobalHealthStateManager(t *testing.T) {
	// Reset global state
	globalHealthStateManager = nil

	// Test GetHealthStateManager creates new instance
	hsm1 := GetHealthStateManager()
	if hsm1 == nil {
		t.Fatal("Expected GetHealthStateManager to return non-nil")
	}

	// Test GetHealthStateManager returns same instance
	hsm2 := GetHealthStateManager()
	if hsm1 != hsm2 {
		t.Error("Expected GetHealthStateManager to return same instance")
	}

	// Test state operations work
	hsm1.SetControllerReady()
	state := hsm1.GetState()
	if state.ControllerReady != 1 {
		t.Errorf("Expected ControllerReady to be 1, got %d", state.ControllerReady)
	}
}
