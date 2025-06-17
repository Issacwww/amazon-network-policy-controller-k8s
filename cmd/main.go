/*
Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"net/http"
	"os"

	"github.com/go-logr/logr"
	"github.com/spf13/pflag"
	"go.uber.org/zap/zapcore"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "net/http/pprof"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	policyinfo "github.com/aws/amazon-network-policy-controller-k8s/api/v1alpha1"
	"github.com/aws/amazon-network-policy-controller-k8s/internal/controllers"
	"github.com/aws/amazon-network-policy-controller-k8s/pkg/config"
	"github.com/aws/amazon-network-policy-controller-k8s/pkg/crd"
	"github.com/aws/amazon-network-policy-controller-k8s/pkg/health"
	"github.com/aws/amazon-network-policy-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-network-policy-controller-k8s/pkg/policyendpoints"
	"github.com/aws/amazon-network-policy-controller-k8s/pkg/utils/configmap"
	"github.com/aws/amazon-network-policy-controller-k8s/pkg/version"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(policyinfo.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	infoLogger := getLoggerWithLogLevel("info")
	infoLogger.Info("version",
		"GitVersion", version.GitVersion,
		"GitCommit", version.GitCommit,
		"BuildDate", version.BuildDate,
	)
	controllerCFG, err := loadControllerConfig()
	if err != nil {
		infoLogger.Error(err, "unable to load controller config")
		os.Exit(1) // This is acceptable as health server hasn't started yet
	}
	ctrlLogger := getLoggerWithLogLevel(controllerCFG.LogLevel)
	ctrl.SetLogger(ctrlLogger)

	// Start custom health server early, independent of controller manager
	healthLogger := ctrl.Log.WithName("health-server")
	if err := health.StartHealthServer(controllerCFG.CustomHealthPort, healthLogger); err != nil {
		setupLog.Error(err, "unable to start custom health server")
		os.Exit(1) // This is acceptable as health server failed to start
	}
	defer func() {
		if err := health.StopHealthServer(context.Background()); err != nil {
			setupLog.Error(err, "error stopping health server")
		}
	}()

	// Get health state manager for error reporting
	healthStateMgr := health.GetHealthStateManager()

	restCFG, err := config.BuildRestConfig(controllerCFG.RuntimeConfig)
	if err != nil {
		setupLog.Error(err, "unable to build REST config")
		healthStateMgr.SetFatalError("unable to build REST config")
		runHealthServerOnly(healthStateMgr)
		return
	}

	clientSetRestConfig, err := config.BuildRestConfig(controllerCFG.RuntimeConfig)
	if err != nil {
		setupLog.Error(err, "unable to build REST config")
		healthStateMgr.SetFatalError("unable to build REST config")
		runHealthServerOnly(healthStateMgr)
		return
	}
	clientSetRestConfig.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
	clientSetRestConfig.ContentType = "application/vnd.kubernetes.protobuf"

	clientSet, err := kubernetes.NewForConfig(clientSetRestConfig)
	if err != nil {
		setupLog.Error(err, "unable to obtain clientSet")
		healthStateMgr.SetFatalError("unable to obtain clientSet")
		runHealthServerOnly(healthStateMgr)
		return
	}

	// Install CRDs before starting the controller
	setupLog.Info("Installing required CRDs")
	crdInstaller, err := crd.NewCRDInstaller(restCFG, ctrl.Log.WithName("crd-installer"))
	if err != nil {
		setupLog.Error(err, "unable to create CRD installer")
		healthStateMgr.SetFatalError("unable to create CRD installer")
		runHealthServerOnly(healthStateMgr)
		return
	}

	ctx := ctrl.SetupSignalHandler()
	if err := crdInstaller.InstallCRDs(ctx); err != nil {
		setupLog.Error(err, "unable to install CRDs")
		healthStateMgr.SetFatalError("unable to install CRDs")
		runHealthServerOnly(healthStateMgr)
		return
	}
	setupLog.Info("CRD installation completed successfully")

	enableNetworkPolicyController := true
	setupLog.Info("Checking args for enabling CM", "ConfigMapEnabled", controllerCFG.EnableConfigMapCheck)
	setupLog.Info("Checking args for PE chunk size", "PEChunkSize", controllerCFG.EndpointChunkSize)
	setupLog.Info("Checking args for policy batch time", "NPBatchTime", controllerCFG.PodUpdateBatchPeriodDuration)
	setupLog.Info("Checking args for reconciler count", "ReconcilerCount", controllerCFG.MaxConcurrentReconciles)

	if controllerCFG.EnableConfigMapCheck {
		var cancelFn context.CancelFunc
		ctx, cancelFn = context.WithCancel(ctx)
		setupLog.Info("Enable network policy controller based on configuration", "configmap", configmap.GetControllerConfigMapId())
		configMapManager := config.NewConfigmapManager(configmap.GetControllerConfigMapId(),
			clientSet, cancelFn, configmap.GetConfigmapCheckFn(), ctrl.Log.WithName("configmap-manager"))
		
		// Integrate health state management with configmap manager
		configMapManager.SetHealthStateManager(healthStateMgr)
		
		if err := configMapManager.MonitorConfigMap(ctx); err != nil {
			setupLog.Error(err, "Unable to monitor configmap for checking if controller is enabled")
			healthStateMgr.SetFatalError("unable to monitor configmap")
			runHealthServerOnly(healthStateMgr)
			return
		}
		enableNetworkPolicyController = configMapManager.IsControllerEnabled()
		if !enableNetworkPolicyController {
			setupLog.Info("Disabling leader election since network policy controller is not enabled")
			controllerCFG.RuntimeConfig.EnableLeaderElection = false
		}
	}

	rtOpts := config.BuildRuntimeOptions(controllerCFG.RuntimeConfig, scheme)

	mgr, err := ctrl.NewManager(restCFG, rtOpts)
	if err != nil {
		setupLog.Error(err, "unable to create controller manager")
		healthStateMgr.SetFatalError("unable to create controller manager")
		runHealthServerOnly(healthStateMgr)
		return
	}

	policyEndpointsManager := policyendpoints.NewPolicyEndpointsManager(mgr.GetClient(),
		controllerCFG.EndpointChunkSize, ctrl.Log.WithName("endpoints-manager"))
	finalizerManager := k8s.NewDefaultFinalizerManager(mgr.GetClient(), ctrl.Log.WithName("finalizer-manager"))
	policyController := controllers.NewPolicyReconciler(mgr.GetClient(), policyEndpointsManager,
		controllerCFG, finalizerManager, ctrl.Log.WithName("controllers").WithName("policy"))
	if enableNetworkPolicyController {
		setupLog.Info("Network Policy controller is enabled, starting watches")
		if err := policyController.SetupWithManager(ctx, mgr); err != nil {
			setupLog.Error(err, "Unable to setup network policy controller")
			healthStateMgr.SetFatalError("unable to setup network policy controller")
			runHealthServerOnly(healthStateMgr)
			return
		}
	}

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1) // This is acceptable as it's a setup error
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1) // This is acceptable as it's a setup error
	}

	// Mark controller as ready after successful setup
	healthStateMgr.SetControllerReady()
	setupLog.Info("Controller setup completed successfully")

	if controllerCFG.EnableGoProfiling {
		go func() {
			if err := http.ListenAndServe("localhost:6060", nil); err != nil {
				setupLog.Error(err, "Error starting HTTP server")
			}
		}()
	}

	setupLog.Info("starting controller manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running controller manager")
		healthStateMgr.SetFatalError("controller manager failed")
		runHealthServerOnly(healthStateMgr)
		return
	}
	setupLog.Info("controller manager stopped")
}

// runHealthServerOnly runs only the health server when controller setup fails
func runHealthServerOnly(healthStateMgr *health.HealthStateManager) {
	setupLog.Info("Running in health-server-only mode due to setup errors")

	// Keep the health server running to report the error state
	// This allows monitoring systems to detect the issue without thinking the service crashed
	select {} // Block forever, keeping the health server alive
}

// loadControllerConfig loads the controller configuration
func loadControllerConfig() (config.ControllerConfig, error) {
	controllerConfig := config.ControllerConfig{}
	fs := pflag.NewFlagSet("", pflag.ExitOnError)
	controllerConfig.BindFlags(fs)

	if err := fs.Parse(os.Args); err != nil {
		return controllerConfig, err
	}

	return controllerConfig, nil
}

// getLoggerWithLogLevel returns logger with specific log level.
func getLoggerWithLogLevel(logLevel string) logr.Logger {
	var zapLevel zapcore.Level
	switch logLevel {
	case "info":
		zapLevel = zapcore.InfoLevel
	case "debug":
		zapLevel = zapcore.DebugLevel
	default:
		zapLevel = zapcore.InfoLevel
	}
	return zap.New(zap.UseDevMode(false),
		zap.Level(zapLevel),
		zap.StacktraceLevel(zapcore.FatalLevel),
	)
}
