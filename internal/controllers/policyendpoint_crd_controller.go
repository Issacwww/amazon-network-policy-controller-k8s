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

package controllers

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/go-logr/logr"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/internalversion/scheme"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const policyendpointsCrdName = "policyendpoints.networking.k8s.aws"

//go:embed crds.yaml
var policyEndpointsCrd string

var desiredCRD *apiextensionsv1.CustomResourceDefinition

func init() {
	obj, err := decodePolicyEndpointsCrd()
	if err != nil {
		// Since this is during package initialization, we should panic if we can't load the CRD
		panic(fmt.Errorf("failed to decode CRD from embedded YAML: %w", err))
	}
	desiredCRD = obj
}

func decodePolicyEndpointsCrd() (*apiextensionsv1.CustomResourceDefinition, error) {
	decoder := scheme.Codecs.UniversalDeserializer()
	obj := &apiextensionsv1.CustomResourceDefinition{}

	_, _, err := decoder.Decode([]byte(policyEndpointsCrd), nil, obj)
	if err != nil {
		return nil, fmt.Errorf("failed to decode CRD from embedded YAML: %w", err)
	}
	return obj, nil
}

func NewPolicyEndpointCRDReconciler(k8sClient client.Client, logger logr.Logger) *PolicyEndpointCRDReconciler {
	return &PolicyEndpointCRDReconciler{
		k8sClient: k8sClient,
		logger:    logger,
	}
}

// PolicyEndpointCRDReconciler reconciles a CRD object
type PolicyEndpointCRDReconciler struct {
	k8sClient client.Client
	logger    logr.Logger
}

// +kubebuilder:rbac:groups=extensions,resources=crds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=extensions,resources=crds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=extensions,resources=crds/finalizers,verbs=update
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch;create;update

func (r *PolicyEndpointCRDReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = logf.FromContext(ctx)
	r.logger.Info("Got reconcile request", "resource", req)
	if req.Name != policyendpointsCrdName {
		r.logger.Info("Ignoring reconcile request for non-policyendpoints CRD", "name", req.Name)
		return ctrl.Result{}, nil
	}

	existing := &apiextensionsv1.CustomResourceDefinition{}
	err := r.k8sClient.Get(ctx, types.NamespacedName{Name: desiredCRD.Name}, existing)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			r.logger.Info("Policy Endpoint CRD not found, creating...")
			return ctrl.Result{}, r.k8sClient.Create(ctx, desiredCRD)
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyEndpointCRDReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&apiextensionsv1.CustomResourceDefinition{}).
		Named("policyEndpointCrd").
		Complete(r)
}
