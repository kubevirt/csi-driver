package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v2"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	klog "k8s.io/klog/v2"

	snapcli "kubevirt.io/csi-driver/pkg/generated/external-snapshotter/client-go/clientset/versioned"
	"kubevirt.io/csi-driver/pkg/kubevirt"
	"kubevirt.io/csi-driver/pkg/service"
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
}

func main() {
	// Pass arguments to parseConfig skipping the binary name.
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		os.Exit(1)
	}

	if err := prechecks(cfg); err != nil {
		klog.Fatalf("Prechecks failed: %s", err)
	}

	driver, err := initializeDriver(cfg)
	if err != nil {
		klog.Fatalf("Failed to initialize driver: %s", err)
	}

	driver.Run(cfg.endpoint)
	os.Exit(0)
}

// parseConfig builds the required config struct to run the driver based on the
// flags and environment variables.
func parseConfig(args []string) (*config, error) {
	fs := flag.NewFlagSet("kubevirt-csi-driver", flag.ContinueOnError)
	klog.InitFlags(fs)

	cfg := &config{}
	fs.StringVar(&cfg.endpoint, "endpoint", "unix:/csi/csi.sock", "CSI endpoint")
	fs.StringVar(&cfg.nodeName, "node-name", "", "The node name - the node this pods runs on")
	fs.StringVar(&cfg.infraClusterNamespace, "infra-cluster-namespace", "", "The infra-cluster namespace")
	fs.StringVar(&cfg.infraClusterKubeconfig, "infra-cluster-kubeconfig", "", "the infra-cluster kubeconfig file. If not set, defaults to in cluster config.")
	fs.StringVar(&cfg.infraClusterLabels, "infra-cluster-labels", "", "The infra-cluster labels to use when creating resources in infra cluster. 'name=value' fields separated by a comma")
	fs.StringVar(&cfg.volumePrefix, "volume-prefix", "pvc", "The prefix expected for persistent volumes")

	fs.StringVar(&cfg.tenantClusterKubeconfig, "tenant-cluster-kubeconfig", "", "the tenant cluster kubeconfig file. If not set, defaults to in cluster config.")

	fs.BoolVar(&cfg.runNodeService, "run-node-service", true, "Specifies whether or not to run the node service, the default is true")
	fs.BoolVar(&cfg.runControllerService, "run-controller-service", true, "Specifies whether or not to run the controller service, the default is true")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg.infraStorageClassEnforcement = os.Getenv("INFRA_STORAGE_CLASS_ENFORCEMENT")

	return cfg, nil
}

// prechecks performs validation checks on the configuration provided.
// prechecks will log any error and exit.
func prechecks(cfg *config) error {
	if service.VendorVersion == "" {
		return errors.New("VendorVersion must be set at compile time")
	}

	if cfg.infraClusterLabels == "" && !cfg.runNodeService {
		return errors.New("infra-cluster-labels must be set")
	}

	if cfg.runControllerService && cfg.volumePrefix == "" {
		return errors.New("volume-prefix must be set when deploying the controller")
	}

	if cfg.runNodeService && cfg.nodeName == "" {
		return errors.New("cannot start NodeService without a node name.")
	}

	return nil
}

// initializeDriver instantiates a KubeVirtCSIDriver.
func initializeDriver(cfg *config) (*service.KubevirtCSIDriver, error) {
	klog.V(2).Infof("Driver vendor %v %v", service.VendorName, service.VendorVersion)

	driver := service.NewKubevirtCSIDriver()
	var err error

	if cfg.runControllerService {
		driver, err = configureControllerService(cfg, driver)
		if err != nil {
			return nil, fmt.Errorf("failed to configure controller service: %w", err)
		}
	}

	if cfg.runNodeService {
		driver, err = configureNodeService(cfg, driver)
		if err != nil {
			return nil, fmt.Errorf("failed to configure node service: %w", err)
		}
	}

	return driver, nil
}

// configureControllerService prepares the required clients and configuration for a
// KubeVirtCSIDriver with controller service.
func configureControllerService(cfg *config, driver *service.KubevirtCSIDriver) (*service.KubevirtCSIDriver, error) {
	// Configure labels and storage class enforcement.
	infraClusterLabelsMap, err := parseLabels(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse labels: %w", err)
	}
	klog.V(5).Infof("Storage class enforcement string: \n%s", cfg.infraStorageClassEnforcement)
	storageClassEnforcement, err := configureStorageClassEnforcement(cfg.infraStorageClassEnforcement)
	if err != nil {
		return nil, fmt.Errorf("failed to configure storage class enforcement: %w", err)
	}

	// Get rest configs.
	infraRestConfig, err := getConfigOrInCluster(cfg.infraClusterKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get infra rest config: %w", err)
	}
	tenantRestConfig, err := getConfigOrInCluster(cfg.tenantClusterKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get tenant rest config: %w", err)
	}

	// Generate required clientsets.
	tenantClientset, err := kubernetes.NewForConfig(tenantRestConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build tenant client set: %w", err)
	}
	tenantSnapshotClientset, err := snapcli.NewForConfig(tenantRestConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build tenant snapshot client set: %w", err)
	}
	identityClientset, err := kubernetes.NewForConfig(infraRestConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build infra client set: %w", err)
	}

	// Initialize virt client.
	virtClient, err := kubevirt.NewClient(
		infraRestConfig,
		infraClusterLabelsMap,
		tenantClientset,
		tenantSnapshotClientset,
		storageClassEnforcement,
		cfg.volumePrefix,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize virt client: %w", err)
	}

	return driver.
		WithControllerService(
			virtClient,
			cfg.infraClusterNamespace,
			infraClusterLabelsMap,
			storageClassEnforcement,
		).
		WithIdentityService(
			identityClientset,
		), nil
}

// configureNodeService prepares the required clients and configuration for a
// KubeVirtCSIDriver with node service.
func configureNodeService(cfg *config, driver *service.KubevirtCSIDriver) (*service.KubevirtCSIDriver, error) {
	// Generate tenant clientset.
	tenantRestConfig, err := getConfigOrInCluster(cfg.tenantClusterKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get tenant rest config: %w", err)
	}
	tenantClientset, err := kubernetes.NewForConfig(tenantRestConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build tenant client set: %w", err)
	}

	// Get node ID.
	node, err := tenantClientset.CoreV1().Nodes().Get(context.Background(), cfg.nodeName, v1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to find node by name %v: %w", cfg.nodeName, err)
	}
	nodeID, err := resolveNodeID(node.Spec.ProviderID, node.Annotations)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve infra VM for node %q: %w", cfg.nodeName, err)
	}
	klog.Infof("Node name: %q, Node ID: %q", cfg.nodeName, nodeID)

	return driver.
		WithNodeService(
			nodeID,
		).
		WithIdentityService(
			tenantClientset,
		), nil
}

func configureStorageClassEnforcement(infraStorageClassEnforcement string) (util.StorageClassEnforcement, error) {
	if infraStorageClassEnforcement == "" {
		return util.StorageClassEnforcement{
			AllowAll:     true,
			AllowDefault: true,
		}, nil
	}

	// Parse yaml
	var storageClassEnforcement util.StorageClassEnforcement
	err := yaml.Unmarshal([]byte(infraStorageClassEnforcement), &storageClassEnforcement)
	if err != nil {
		return storageClassEnforcement, fmt.Errorf("failed to parse infra-storage-class-enforcement %w", err)
	}
	return storageClassEnforcement, nil
}

func parseLabels(cfg *config) (map[string]string, error) {
	infraClusterLabelsMap := map[string]string{}

	if cfg.infraClusterLabels == "" {
		return infraClusterLabelsMap, nil
	}

	labelStrings := strings.Split(cfg.infraClusterLabels, ",")
	for _, label := range labelStrings {
		labelPair := strings.SplitN(label, "=", 2)
		if len(labelPair) != 2 {
			return nil, errors.New("bad labels format. Should be 'key=value,key=value,...'")
		}
		infraClusterLabelsMap[labelPair[0]] = labelPair[1]
	}

	return infraClusterLabelsMap, nil
}

// resolveNodeID resolves the infra cluster VM name and namespace from the node's providerID or annotations.
// It returns the nodeID in the format "namespace/name" or an error if resolution fails.
func resolveNodeID(providerID string, annotations map[string]string) (string, error) {
	var vmName, vmNamespace string

	if strings.HasPrefix(providerID, "kubevirt://") {
		vmName = strings.TrimPrefix(providerID, "kubevirt://")
		if annotations != nil {
			vmNamespace = annotations["cluster.x-k8s.io/cluster-namespace"]
		}
	} else {
		// Fallback to annotations if providerID is empty or not kubevirt://
		if annotations != nil {
			vmName = annotations["csi.kubevirt.io/infra-vm-name"]
			vmNamespace = annotations["csi.kubevirt.io/infra-vm-namespace"]
		}
	}

	if vmName == "" || vmNamespace == "" {
		return "", errors.New("vmName or vmNamespace not found. " +
			"Ensure the node has a valid providerID (kubevirt://) with annotation 'cluster.x-k8s.io/cluster-namespace', " +
			"or set annotations 'csi.kubevirt.io/infra-vm-name' and 'csi.kubevirt.io/infra-vm-namespace' on the node. " +
			"After setting the annotations, restart the kubevirt-csi-node pod on this node for the changes to take effect")
	}

	return fmt.Sprintf("%s/%s", vmNamespace, vmName), nil
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
