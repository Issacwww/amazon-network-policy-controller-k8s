package utils

import (
	"fmt"
	"net/http"

	"github.com/aws/amazon-network-policy-controller-k8s/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

// PolicyEndpointCRDHealthzChecker returns a healthz.Checker that checks if the PolicyEndpoint CRD exists.
func PolicyEndpointCRDHealthzChecker(c client.Client) healthz.Checker {
	return func(req *http.Request) error {
		ctx := req.Context()
		list := &v1alpha1.PolicyEndpointList{}
		err := c.List(ctx, list)
		if err != nil {
			if isCRDNotFoundError(err) {
				return fmt.Errorf("PolicyEndpoint CRD not found: %w", err)
			}
			return fmt.Errorf("unknown error checking PolicyEndpoint CRD: %w", err)
		}
		return nil
	}
}

// isCRDNotFoundError returns true if the error indicates the CRD is missing.
func isCRDNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return contains(s, "no matches for kind") ||
		contains(s, "the server could not find the requested resource") ||
		contains(s, "not found") ||
		contains(s, "NotFound")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			contains(s[1:], substr))))
}
