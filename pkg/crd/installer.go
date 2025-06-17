package crd

import (
	"context"
	"embed"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"
)

//go:embed crds/*.yaml
var crdFiles embed.FS

// CRDInstaller handles CRD installation
type CRDInstaller struct {
	client     apiextensionsclient.Interface
	logger     logr.Logger
	restConfig *rest.Config
}

// NewCRDInstaller creates a new CRD installer
func NewCRDInstaller(restConfig *rest.Config, logger logr.Logger) (*CRDInstaller, error) {
	client, err := apiextensionsclient.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create apiextensions client: %w", err)
	}

	return &CRDInstaller{
		client:     client,
		logger:     logger,
		restConfig: restConfig,
	}, nil
}

// InstallCRDs installs all required CRDs
func (ci *CRDInstaller) InstallCRDs(ctx context.Context) error {
	ci.logger.Info("Starting CRD installation")

	// Read and install each CRD file
	entries, err := crdFiles.ReadDir("crds")
	if err != nil {
		return fmt.Errorf("failed to read CRD directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}

		ci.logger.Info("Installing CRD", "file", entry.Name())
		if err := ci.installCRDFromFile(ctx, entry.Name()); err != nil {
			return fmt.Errorf("failed to install CRD from %s: %w", entry.Name(), err)
		}
	}

	ci.logger.Info("CRD installation completed successfully")
	return nil
}

// installCRDFromFile installs a single CRD from an embedded file
func (ci *CRDInstaller) installCRDFromFile(ctx context.Context, filename string) error {
	// Read the CRD file
	data, err := crdFiles.ReadFile("crds/" + filename)
	if err != nil {
		return fmt.Errorf("failed to read CRD file %s: %w", filename, err)
	}

	// Parse the YAML
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(data, &crd); err != nil {
		return fmt.Errorf("failed to unmarshal CRD from %s: %w", filename, err)
	}

	// Install the CRD
	return ci.installCRD(ctx, &crd)
}

// installCRD installs a single CRD
func (ci *CRDInstaller) installCRD(ctx context.Context, crd *apiextensionsv1.CustomResourceDefinition) error {
	ci.logger.Info("Installing CRD", "name", crd.Name, "group", crd.Spec.Group, "version", crd.Spec.Versions[0].Name)

	// Check if CRD already exists
	existing, err := ci.client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, crd.Name, metav1.GetOptions{})
	if err == nil {
		ci.logger.Info("CRD already exists, updating", "name", crd.Name)
		// Update existing CRD
		crd.ResourceVersion = existing.ResourceVersion
		_, err = ci.client.ApiextensionsV1().CustomResourceDefinitions().Update(ctx, crd, metav1.UpdateOptions{})
	} else {
		ci.logger.Info("Creating new CRD", "name", crd.Name)
		// Create new CRD
		_, err = ci.client.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{})
	}

	if err != nil {
		return fmt.Errorf("failed to install CRD %s: %w", crd.Name, err)
	}

	// Wait for CRD to be established
	return ci.waitForCRDEstablished(ctx, crd.Name)
}

// waitForCRDEstablished waits for a CRD to be established
func (ci *CRDInstaller) waitForCRDEstablished(ctx context.Context, crdName string) error {
	ci.logger.Info("Waiting for CRD to be established", "name", crdName)

	return wait.PollUntilContextTimeout(ctx, 2*time.Second, 60*time.Second, true, func(ctx context.Context) (bool, error) {
		crd, err := ci.client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, crdName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		for _, condition := range crd.Status.Conditions {
			if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
				ci.logger.Info("CRD established successfully", "name", crdName)
				return true, nil
			}
		}

		return false, nil
	})
}

// isYAMLFile checks if a file is a YAML file
func isYAMLFile(filename string) bool {
	return len(filename) > 5 && (filename[len(filename)-5:] == ".yaml" || filename[len(filename)-4:] == ".yml")
}
