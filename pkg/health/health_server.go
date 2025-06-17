package health

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
)

const (
	DefaultHealthPort = ":8081"
	HealthEndpoint    = "/healthz/custom"
)

// HealthServer provides a standalone health check service
type HealthServer struct {
	port     string
	stateMgr *HealthStateManager
	logger   logr.Logger
	server   *http.Server
}

// NewHealthServer creates a new health server
func NewHealthServer(port string, stateMgr *HealthStateManager, logger logr.Logger) *HealthServer {
	if port == "" {
		port = DefaultHealthPort
	}

	hs := &HealthServer{
		port:     port,
		stateMgr: stateMgr,
		logger:   logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc(HealthEndpoint, hs.healthHandler)

	hs.server = &http.Server{
		Addr:         port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	return hs
}

// Start starts the health server in a goroutine
func (hs *HealthServer) Start() error {
	hs.logger.Info("Starting health server", "port", hs.port, "endpoint", HealthEndpoint)

	go func() {
		if err := hs.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			hs.logger.Error(err, "Health server failed to start")
			// Set fatal error state if server fails to start
			hs.stateMgr.SetFatalError(fmt.Sprintf("Health server failed to start: %v", err))
		}
	}()

	return nil
}

// Stop gracefully stops the health server
func (hs *HealthServer) Stop(ctx context.Context) error {
	hs.logger.Info("Stopping health server")
	return hs.server.Shutdown(ctx)
}

// healthHandler handles health check requests
func (hs *HealthServer) healthHandler(w http.ResponseWriter, r *http.Request) {
	state := hs.stateMgr.GetState()

	// Set content type
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	// Determine response based on health state
	switch {
	case state.FatalError != "":
		// Fatal error - return 500
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "fatal: %s", state.FatalError)
		hs.logger.V(1).Info("Health check - fatal error", "error", state.FatalError)

	case state.DegradedReason != "":
		// Degraded state - return 424 (Failed Dependency)
		w.WriteHeader(http.StatusFailedDependency)
		fmt.Fprintf(w, "degraded: %s", state.DegradedReason)
		hs.logger.V(1).Info("Health check - degraded", "reason", state.DegradedReason)

	case state.ControllerReady == 1:
		// Healthy state - return 200
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
		hs.logger.V(1).Info("Health check - healthy")

	default:
		// Not ready - return 503
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "not ready")
		hs.logger.V(1).Info("Health check - not ready")
	}
}

// Global health state manager instance
var globalHealthStateManager *HealthStateManager

// Global health server instance
var globalHealthServer *HealthServer

// StartHealthServer starts the global health server
func StartHealthServer(port string, logger logr.Logger) error {
	if globalHealthStateManager == nil {
		globalHealthStateManager = NewHealthStateManager()
	}

	if globalHealthServer == nil {
		globalHealthServer = NewHealthServer(port, globalHealthStateManager, logger)
	}

	return globalHealthServer.Start()
}

// GetHealthStateManager returns the global health state manager
func GetHealthStateManager() *HealthStateManager {
	if globalHealthStateManager == nil {
		globalHealthStateManager = NewHealthStateManager()
	}
	return globalHealthStateManager
}

// StopHealthServer stops the global health server
func StopHealthServer(ctx context.Context) error {
	if globalHealthServer != nil {
		return globalHealthServer.Stop(ctx)
	}
	return nil
}
