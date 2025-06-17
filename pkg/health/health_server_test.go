package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestHealthServer(t *testing.T) {
	// Create a test logger
	logger := logr.Discard()

	// Test cases
	testCases := []struct {
		name           string
		setupState     func(*HealthStateManager)
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "healthy state",
			setupState: func(hsm *HealthStateManager) {
				hsm.SetControllerReady()
			},
			expectedStatus: http.StatusOK,
			expectedBody:   "ok",
		},
		{
			name: "degraded state",
			setupState: func(hsm *HealthStateManager) {
				hsm.SetDegraded("missing CRD")
			},
			expectedStatus: http.StatusFailedDependency,
			expectedBody:   "degraded: missing CRD",
		},
		{
			name: "fatal error state",
			setupState: func(hsm *HealthStateManager) {
				hsm.SetFatalError("panic occurred")
			},
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "fatal: panic occurred",
		},
		{
			name:           "not ready state",
			setupState:     func(hsm *HealthStateManager) {},
			expectedStatus: http.StatusServiceUnavailable,
			expectedBody:   "not ready",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create health state manager
			hsm := NewHealthStateManager()
			tc.setupState(hsm)

			// Create health server
			hs := NewHealthServer(":0", hsm, logger)

			// Create test request
			req := httptest.NewRequest("GET", HealthEndpoint, nil)
			w := httptest.NewRecorder()

			// Call health handler
			hs.healthHandler(w, req)

			// Check status code
			if w.Code != tc.expectedStatus {
				t.Errorf("Expected status code %d, got %d", tc.expectedStatus, w.Code)
			}

			// Check response body
			body := w.Body.String()
			if body != tc.expectedBody {
				t.Errorf("Expected body '%s', got '%s'", tc.expectedBody, body)
			}

			// Check content type
			contentType := w.Header().Get("Content-Type")
			expectedContentType := "text/plain; charset=utf-8"
			if contentType != expectedContentType {
				t.Errorf("Expected content type '%s', got '%s'", expectedContentType, contentType)
			}
		})
	}
}

func TestHealthServerStartStop(t *testing.T) {
	logger := logr.Discard()
	hsm := NewHealthStateManager()
	hs := NewHealthServer(":0", hsm, logger)

	// Test start
	err := hs.Start()
	if err != nil {
		t.Fatalf("Failed to start health server: %v", err)
	}

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = hs.Stop(ctx)
	if err != nil {
		t.Errorf("Failed to stop health server: %v", err)
	}
}

func TestGlobalHealthServer(t *testing.T) {
	// Reset global state
	globalHealthStateManager = nil
	globalHealthServer = nil

	logger := logr.Discard()

	// Test StartHealthServer
	err := StartHealthServer(":0", logger)
	if err != nil {
		t.Fatalf("Failed to start global health server: %v", err)
	}

	// Test GetHealthStateManager
	hsm := GetHealthStateManager()
	if hsm == nil {
		t.Fatal("Expected GetHealthStateManager to return non-nil")
	}

	// Test state operations
	hsm.SetControllerReady()
	state := hsm.GetState()
	if state.ControllerReady != 1 {
		t.Errorf("Expected ControllerReady to be 1, got %d", state.ControllerReady)
	}

	// Test StopHealthServer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = StopHealthServer(ctx)
	if err != nil {
		t.Errorf("Failed to stop global health server: %v", err)
	}
}

func TestHealthServerWithRealPort(t *testing.T) {
	logger := logr.Discard()
	hsm := NewHealthStateManager()
	hs := NewHealthServer(":8082", hsm, logger)

	// Start server
	err := hs.Start()
	if err != nil {
		t.Fatalf("Failed to start health server: %v", err)
	}

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test health endpoint
	resp, err := http.Get("http://localhost:8082" + HealthEndpoint)
	if err != nil {
		t.Fatalf("Failed to make request to health endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Expected status code %d, got %d", http.StatusServiceUnavailable, resp.StatusCode)
	}

	// Stop server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = hs.Stop(ctx)
	if err != nil {
		t.Errorf("Failed to stop health server: %v", err)
	}
}
