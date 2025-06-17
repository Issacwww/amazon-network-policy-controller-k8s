package config

import (
	"context"
	"errors"

	"github.com/aws/amazon-network-policy-controller-k8s/pkg/health"
	"github.com/aws/amazon-network-policy-controller-k8s/pkg/k8s"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// +kubebuilder:rbac:groups="",resources=configmaps,namespace="system",resourceNames=amazon-vpc-cni,verbs=get;list;watch

type ConfigmapManager interface {
	MonitorConfigMap(ctx context.Context) error
	IsControllerEnabled() bool
	// Health state management methods
	SetHealthStateManager(healthMgr *health.HealthStateManager)
	GetHealthStateManager() *health.HealthStateManager
}

var _ ConfigmapManager = (*defaultConfigmapManager)(nil)

type defaultConfigmapManager struct {
	initialState           bool
	resourceRef            types.NamespacedName
	store                  cache.Store
	rt                     *cache.Reflector
	clientSet              *kubernetes.Clientset
	logger                 logr.Logger
	cancelFn               context.CancelFunc
	monitorStopChan        chan struct{}
	storeNotifyChan        chan struct{}
	configMapCheckFunction func(*corev1.ConfigMap) bool
	// Health state management
	healthStateMgr *health.HealthStateManager
}

func NewConfigmapManager(resourceRef types.NamespacedName, clientSet *kubernetes.Clientset,
	cancelFn context.CancelFunc, configmapCheckFunction func(configMap *corev1.ConfigMap) bool, logger logr.Logger) *defaultConfigmapManager {
	storeNotifyChan := make(chan struct{})
	cmStore := k8s.NewConfigMapStore(storeNotifyChan)
	return &defaultConfigmapManager{
		clientSet:              clientSet,
		resourceRef:            resourceRef,
		store:                  cmStore,
		logger:                 logger,
		cancelFn:               cancelFn,
		monitorStopChan:        make(chan struct{}),
		storeNotifyChan:        storeNotifyChan,
		configMapCheckFunction: configmapCheckFunction,
	}
}

// SetHealthStateManager sets the health state manager for this configmap manager
func (m *defaultConfigmapManager) SetHealthStateManager(healthMgr *health.HealthStateManager) {
	m.healthStateMgr = healthMgr
}

// GetHealthStateManager returns the health state manager
func (m *defaultConfigmapManager) GetHealthStateManager() *health.HealthStateManager {
	return m.healthStateMgr
}

// IsControllerEnabled returns the initial state of the policy controller.
func (m *defaultConfigmapManager) IsControllerEnabled() bool {
	m.logger.V(1).Info("IsControllerEnabled() returning", "value", m.initialState)
	return m.initialState
}

// MonitorConfigMap starts cache reflector and watches for configmap updates.
func (m *defaultConfigmapManager) MonitorConfigMap(ctx context.Context) error {
	fieldSelector := fields.Set{"metadata.name": m.resourceRef.Name}.AsSelector().String()
	listFunc := func(options metav1.ListOptions) (runtime.Object, error) {
		options.FieldSelector = fieldSelector
		return m.clientSet.CoreV1().ConfigMaps(m.resourceRef.Namespace).List(ctx, options)
	}
	watchFunc := func(options metav1.ListOptions) (watch.Interface, error) {
		options.FieldSelector = fieldSelector
		return m.clientSet.CoreV1().ConfigMaps(m.resourceRef.Namespace).Watch(ctx, options)
	}
	m.rt = cache.NewReflector(&cache.ListWatch{ListFunc: listFunc, WatchFunc: watchFunc},
		&corev1.ConfigMap{},
		m.store,
		0,
	)
	go m.rt.Run(m.monitorStopChan)
	go m.listenForConfigMapUpdates()

	if _, err := m.setInitialControllerState(); err != nil {
		m.logger.Info("Failed to set initial state", "err", err)
		// Report degraded state if configmap is missing
		if m.healthStateMgr != nil {
			m.healthStateMgr.SetDegraded("ConfigMap not found or invalid")
		}
		return err
	}

	// Clear any degraded state if configmap is available
	if m.healthStateMgr != nil {
		state := m.healthStateMgr.GetState()
		if state.DegradedReason != "" && contains(state.DegradedReason, "ConfigMap") {
			// Clear degraded state for configmap issues
			m.healthStateMgr.ClearDegraded()
		}
	}

	return nil
}

// listen for the messages in the storeNotifyChan in a loop and update the state of the policy controller accordingly.
func (m *defaultConfigmapManager) listenForConfigMapUpdates() {
	defer func() {
		m.logger.Info("Controller detected changes to the configmap, cancelling manager context")
		close(m.monitorStopChan)
		m.cancelFn()
	}()

	for {
		select {
		case <-m.storeNotifyChan:
			enabled, err := m.getCurrentEnabledConfig()
			if err != nil {
				m.logger.Error(err, "Failed to get controller state from configmap")
				// Report degraded state if configmap becomes unavailable
				if m.healthStateMgr != nil {
					m.healthStateMgr.SetDegraded("ConfigMap not found or invalid")
				}
				return
			}
			m.logger.V(1).Info("Received configmap notification", "initial", m.initialState,
				"new", enabled)
			if m.initialState != enabled {
				m.logger.Info("Controller state changed", "initial", m.initialState,
					"new", enabled)
				return
			}
		}
	}
}

// getCurrentEnabledConfig gets the current state of the policy controller from the configmap
func (m *defaultConfigmapManager) getCurrentEnabledConfig() (bool, error) {
	cm, exists, err := m.store.GetByKey(m.resourceRef.String())
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}
	return m.configMapCheckFunction(cm.(*corev1.ConfigMap)), nil
}

// setInitialControllerState sets the initial state of the policy controller based on the configmap
func (m *defaultConfigmapManager) setInitialControllerState() (retVal bool, err error) {
	defer func() {
		m.logger.V(1).Info("setInitialControllerState", "retVal", retVal, "err", err)
		m.initialState = retVal
	}()
	// Wait for cache sync
	if !cache.WaitForCacheSync(m.monitorStopChan, func() bool {
		return m.rt.LastSyncResourceVersion() != ""
	}) {
		return false, errors.New("failed to sync configmap cache")
	}
	return m.getCurrentEnabledConfig()
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			contains(s[1:], substr))))
}
