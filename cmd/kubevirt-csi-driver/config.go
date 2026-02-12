package main

import (
	"errors"
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	snapcli "kubevirt.io/csi-driver/pkg/generated/external-snapshotter/client-go/clientset/versioned"
	"kubevirt.io/csi-driver/pkg/kubevirt"
	"kubevirt.io/csi-driver/pkg/util"
)

type config struct {
	endpoint                     string
	nodeName                     string
	infraClusterNamespace        string
	infraClusterKubeconfig       string
	infraClusterLabels           string
	volumePrefix                 string
	infraStorageClassEnforcement string

	tenantClusterKubeconfig string

	runNodeService       bool
	runControllerService bool

	// Client section.
	tenantConfig            *rest.Config
	infraConfig             *rest.Config
	tenantClientset         kubernetes.Interface
	tenantSnapshotClientset snapcli.Interface
	infraClientset          kubernetes.Interface
	virtClient              kubevirt.Client
}

func (c *config) getTenantConfig() (*rest.Config, error) {
	if c.tenantConfig != nil {
		return c.tenantConfig, nil
	}

	rc, err := getConfigOrInCluster(c.tenantClusterKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get tenant rest config: %w", err)
	}

	c.tenantConfig = rc
	return c.tenantConfig, nil
}

func (c *config) getInfraConfig() (*rest.Config, error) {
	if c.infraConfig != nil {
		return c.infraConfig, nil
	}

	rc, err := getConfigOrInCluster(c.infraClusterKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get infra rest config: %w", err)
	}

	c.infraConfig = rc
	return c.infraConfig, nil
}

func (c *config) getTenantClientset() (kubernetes.Interface, error) {
	if c.tenantClientset != nil {
		return c.tenantClientset, nil
	}

	rc, err := c.getTenantConfig()
	if err != nil {
		return nil, err
	}

	tc, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("failed to build tenant client set: %w", err)
	}

	c.tenantClientset = tc
	return c.tenantClientset, nil
}

func (c *config) getTenantSnapshotClientset() (snapcli.Interface, error) {
	if c.tenantSnapshotClientset != nil {
		return c.tenantSnapshotClientset, nil
	}

	rc, err := c.getTenantConfig()
	if err != nil {
		return nil, err
	}

	tc, err := snapcli.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("failed to build tenant snapshot client set: %w", err)
	}

	c.tenantSnapshotClientset = tc
	return c.tenantSnapshotClientset, nil
}

func (c *config) getInfraClientset() (kubernetes.Interface, error) {
	if c.infraClientset != nil {
		return c.infraClientset, nil
	}

	rc, err := c.getInfraConfig()
	if err != nil {
		return nil, err
	}

	tc, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("failed to build infra client set: %w", err)
	}

	c.infraClientset = tc
	return c.infraClientset, nil
}

func (c *config) getVirtClient(
	infraClusterLabelsMap map[string]string,
	storageClassEnforcement util.StorageClassEnforcement,
) (kubevirt.Client, error) {
	if c.virtClient != nil {
		return c.virtClient, nil
	}

	// Perform precheck.
	if c.volumePrefix == "" {
		return nil, errors.New("volume-prefix must be set when deploying the controller")
	}

	// Get rest configs.
	rc, err := c.getInfraConfig()
	if err != nil {
		return nil, err
	}

	// Get required clientsets.
	tc, err := c.getTenantClientset()
	if err != nil {
		return nil, err
	}
	tsc, err := c.getTenantSnapshotClientset()
	if err != nil {
		return nil, err
	}

	// Initialize virt client.
	vc, err := kubevirt.NewClient(
		rc,
		infraClusterLabelsMap,
		tc,
		tsc,
		storageClassEnforcement,
		c.volumePrefix,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize virt client: %w", err)
	}

	c.virtClient = vc
	return c.virtClient, nil
}

// getConfigOrInCluster loads the Kubernetes REST config.
// If the kubeconfig path is empty, it attempts to load the in-cluster configuration.
func getConfigOrInCluster(kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		// Fallback to in cluster config.
		inClusterConfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to build in cluster config: %w", err)
		}
		return inClusterConfig, nil
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build cluster config: %w", err)
	}
	return config, nil
}
