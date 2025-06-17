package health

import (
	"sync/atomic"
)

// HealthState represents the current health state of the controller
type HealthState struct {
	// ControllerReady indicates if the controller has successfully started
	ControllerReady int32
	// DegradedReason stores the reason for degraded state (e.g., missing CRD)
	DegradedReason string
	// FatalError stores any fatal error that occurred
	FatalError string
}

// HealthStateManager manages the health state atomically
type HealthStateManager struct {
	state atomic.Value // stores *HealthState
}

// NewHealthStateManager creates a new health state manager
func NewHealthStateManager() *HealthStateManager {
	hsm := &HealthStateManager{}
	hsm.state.Store(&HealthState{
		ControllerReady: 0,
		DegradedReason:  "",
		FatalError:      "",
	})
	return hsm
}

// SetControllerReady marks the controller as ready
func (hsm *HealthStateManager) SetControllerReady() {
	state := hsm.state.Load().(*HealthState)
	newState := &HealthState{
		ControllerReady: 1,
		DegradedReason:  state.DegradedReason,
		FatalError:      state.FatalError,
	}
	hsm.state.Store(newState)
}

// SetDegraded sets the controller to degraded state with a reason
func (hsm *HealthStateManager) SetDegraded(reason string) {
	state := hsm.state.Load().(*HealthState)
	newState := &HealthState{
		ControllerReady: state.ControllerReady,
		DegradedReason:  reason,
		FatalError:      state.FatalError,
	}
	hsm.state.Store(newState)
}

// ClearDegraded clears the degraded state
func (hsm *HealthStateManager) ClearDegraded() {
	state := hsm.state.Load().(*HealthState)
	newState := &HealthState{
		ControllerReady: state.ControllerReady,
		DegradedReason:  "",
		FatalError:      state.FatalError,
	}
	hsm.state.Store(newState)
}

// SetFatalError sets a fatal error state
func (hsm *HealthStateManager) SetFatalError(err string) {
	state := hsm.state.Load().(*HealthState)
	newState := &HealthState{
		ControllerReady: state.ControllerReady,
		DegradedReason:  state.DegradedReason,
		FatalError:      err,
	}
	hsm.state.Store(newState)
}

// GetState returns the current health state
func (hsm *HealthStateManager) GetState() *HealthState {
	return hsm.state.Load().(*HealthState)
}
