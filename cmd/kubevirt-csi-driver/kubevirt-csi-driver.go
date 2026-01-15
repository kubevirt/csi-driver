package main

import (
	"context"
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

	prechecks(cfg)
	handle(cfg)
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
func prechecks(cfg *config) {
	if service.VendorVersion == "" {
		klog.Fatal("VendorVersion must be set at compile time")
	}

	if cfg.infraClusterLabels == "" && !cfg.runNodeService {
		klog.Fatal("infra-cluster-labels must be set")
	}
	if cfg.volumePrefix == "" {
		klog.Fatal("volume-prefix must be set")
	}

	if cfg.runNodeService && cfg.nodeName == "" {
		klog.Fatal("Cannot start NodeService without a node name.")
	}
}

// handle will instantiate a KubeVirtCSIDriver and start running it.
func handle(cfg *config) {
	klog.V(2).Infof("Driver vendor %v %v", service.VendorName, service.VendorVersion)

	driver := service.NewKubevirtCSIDriver()

	if cfg.runControllerService {
		driver = configureControllerService(cfg, driver)
	}

	if cfg.runNodeService {
		driver = configureNodeService(cfg, driver)
	}

	driver.Run(cfg.endpoint)
}

// configureControllerService prepares the required clients and configuration for a
// KubeVirtCSIDriver with controller service.
func configureControllerService(cfg *config, driver *service.KubevirtCSIDriver) *service.KubevirtCSIDriver {
	// Configure labels and storage class enforcement.
	infraClusterLabelsMap := parseLabels(cfg)
	klog.V(5).Infof("Storage class enforcement string: \n%s", cfg.infraStorageClassEnforcement)
	storageClassEnforcement := configureStorageClassEnforcement(cfg.infraStorageClassEnforcement)

	// Get rest configs.
	infraRestConfig := getConfigOrInCluster(cfg.infraClusterKubeconfig)
	tenantRestConfig := getConfigOrInCluster(cfg.tenantClusterKubeconfig)

	// Generate required clientsets.
	tenantClientSet, err := kubernetes.NewForConfig(tenantRestConfig)
	if err != nil {
		klog.Fatalf("Failed to build tenant client set: %v", err)
	}
	tenantSnapshotClientSet, err := snapcli.NewForConfig(tenantRestConfig)
	if err != nil {
		klog.Fatalf("Failed to build tenant snapshot client set: %v", err)
	}
	identityClientset, err := kubernetes.NewForConfig(infraRestConfig)
	if err != nil {
		klog.Fatalf("Failed to build infra client set: %v", err)
	}

	// Initialize virt client.
	virtClient, err := kubevirt.NewClient(
		infraRestConfig,
		infraClusterLabelsMap,
		tenantClientSet,
		tenantSnapshotClientSet,
		storageClassEnforcement,
		cfg.volumePrefix,
	)
	if err != nil {
		klog.Fatal(err)
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
		)
}

// configureNodeService prepares the required clients and configuration for a
// KubeVirtCSIDriver with node service.
func configureNodeService(cfg *config, driver *service.KubevirtCSIDriver) *service.KubevirtCSIDriver {
	var err error

	// Generate tenant clientset.
	tenantRestConfig := getConfigOrInCluster(cfg.tenantClusterKubeconfig)
	tenantClientSet, err := kubernetes.NewForConfig(tenantRestConfig)
	if err != nil {
		klog.Fatalf("Failed to build tenant client set: %v", err)
	}

	// Get node ID.
	node, err := tenantClientSet.CoreV1().Nodes().Get(context.Background(), cfg.nodeName, v1.GetOptions{})
	if err != nil {
		klog.Fatalf("failed to find node by name %v: %v", cfg.nodeName, err)
	}
	nodeID, err := resolveNodeID(cfg.nodeName, node.Spec.ProviderID, node.Annotations)
	if err != nil {
		klog.Fatal(err)
	}
	klog.Infof("Node name: %v, Node ID: %s", cfg.nodeName, nodeID)

	return driver.
		WithNodeService(
			nodeID,
		).
		WithIdentityService(
			tenantClientSet,
		)
}

func configureStorageClassEnforcement(infraStorageClassEnforcement string) util.StorageClassEnforcement {
	var storageClassEnforcement util.StorageClassEnforcement

	if infraStorageClassEnforcement == "" {
		storageClassEnforcement = util.StorageClassEnforcement{
			AllowAll:     true,
			AllowDefault: true,
		}
	} else {
		//parse yaml
		err := yaml.Unmarshal([]byte(infraStorageClassEnforcement), &storageClassEnforcement)
		if err != nil {
			klog.Fatalf("Failed to parse infra-storage-class-enforcement %v", err)
		}
	}
	return storageClassEnforcement
}

func parseLabels(cfg *config) map[string]string {

	infraClusterLabelsMap := map[string]string{}

	if cfg.infraClusterLabels == "" {
		return infraClusterLabelsMap
	}

	labelStrings := strings.Split(cfg.infraClusterLabels, ",")

	for _, label := range labelStrings {

		labelPair := strings.SplitN(label, "=", 2)

		if len(labelPair) != 2 {
			panic("Bad labels format. Should be 'key=value,key=value,...'")
		}

		infraClusterLabelsMap[labelPair[0]] = labelPair[1]
	}

	return infraClusterLabelsMap
}

// resolveNodeID resolves the infra cluster VM name and namespace from the node's providerID or annotations.
// It returns the nodeID in the format "namespace/name" or an error if resolution fails.
func resolveNodeID(nodeName, providerID string, annotations map[string]string) (string, error) {
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
		return "", fmt.Errorf("failed to resolve infra VM for node %s: vmName or vmNamespace not found. "+
			"Ensure the node has a valid providerID (kubevirt://) with annotation 'cluster.x-k8s.io/cluster-namespace', "+
			"or set annotations 'csi.kubevirt.io/infra-vm-name' and 'csi.kubevirt.io/infra-vm-namespace' on the node. "+
			"After setting the annotations, restart the kubevirt-csi-node pod on this node for the changes to take effect", nodeName)
	}

	return fmt.Sprintf("%s/%s", vmNamespace, vmName), nil
}

// getConfigOrInCluster loads the Kubernetes REST config.
// If the kubeconfig path is empty, it attempts to load the in-cluster configuration.
func getConfigOrInCluster(kubeconfig string) *rest.Config {
	if kubeconfig == "" {
		// Fallback to in cluster config.
		inClusterConfig, err := rest.InClusterConfig()
		if err != nil {
			klog.Fatalf("Failed to build in cluster config: %v", err)
		}
		return inClusterConfig
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		klog.Fatalf("failed to build cluster config: %v", err)
	}
	return config
}
