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

var (
	endpoint               = flag.String("endpoint", "unix:/csi/csi.sock", "CSI endpoint")
	nodeName               = flag.String("node-name", "", "The node name - the node this pods runs on")
	infraClusterNamespace  = flag.String("infra-cluster-namespace", "", "The infra-cluster namespace")
	infraClusterKubeconfig = flag.String("infra-cluster-kubeconfig", "", "the infra-cluster kubeconfig file. If not set, defaults to in cluster config.")
	infraClusterLabels     = flag.String("infra-cluster-labels", "", "The infra-cluster labels to use when creating resources in infra cluster. 'name=value' fields separated by a comma")
	volumePrefix           = flag.String("volume-prefix", "pvc", "The prefix expected for persistent volumes")
	// infraStorageClassEnforcement = flag.String("infra-storage-class-enforcement", "", "A string encoded yaml that represents the policy of enforcing which infra storage classes are allowed in persistentVolume of type kubevirt")
	infraStorageClassEnforcement = os.Getenv("INFRA_STORAGE_CLASS_ENFORCEMENT")

	tenantClusterKubeconfig = flag.String("tenant-cluster-kubeconfig", "", "the tenant cluster kubeconfig file. If not set, defaults to in cluster config.")

	runNodeService       = flag.Bool("run-node-service", true, "Specifies rather or not to run the node service, the default is true")
	runControllerService = flag.Bool("run-controller-service", true, "Specifies rather or not to run the controller service, the default is true")
)

func init() {
	klog.InitFlags(nil)
}

func main() {
	flag.Parse()
	prechecks()
	handle()
	os.Exit(0)
}

// prechecks performs validation checks on the configuration provided.
// prechecks will log any error and exit.
func prechecks() {
	if service.VendorVersion == "" {
		klog.Fatal("VendorVersion must be set at compile time")
	}

	if (infraClusterLabels == nil || *infraClusterLabels == "") && !*runNodeService {
		klog.Fatal("infra-cluster-labels must be set")
	}
	if volumePrefix == nil || *volumePrefix == "" {
		klog.Fatal("volume-prefix must be set")
	}

	if *runNodeService && *nodeName == "" {
		klog.Fatal("Cannot start NodeService without a node name.")
	}
}

// handle will instantiate a KubeVirtCSIDriver and start running it.
func handle() {
	ctx := context.Background()
	klog.V(2).Infof("Driver vendor %v %v", service.VendorName, service.VendorVersion)

	driver := service.NewKubevirtCSIDriver()

	if *runControllerService {
		driver = configureControllerService(ctx, driver)
	}

	if *runNodeService {
		driver = configureNodeService(ctx, driver)
	}

	driver.Run(*endpoint)
}

// configureControllerService will prepare required clients and configuration for a
// KubeVirtCSIDriver with controller service.
//
// Parameters:
//   - driver: A pointer to the KubeVirtCSIDriver to apply the configuration to.
//
// Returns:
//   - KubeVirtCSIDriver: A pointer to the passed driver.
func configureControllerService(ctx context.Context, driver *service.KubevirtCSIDriver) *service.KubevirtCSIDriver {
	// Configure labels and storage class enforcement.
	infraClusterLabelsMap := parseLabels()
	klog.V(5).Infof("Storage class enforcement string: \n%s", infraStorageClassEnforcement)
	storageClassEnforcement := configureStorageClassEnforcement(infraStorageClassEnforcement)

	// Get rest configs.
	infraRestConfig := getConfigOrInCluster(infraClusterKubeconfig)
	tenantRestConfig := getConfigOrInCluster(tenantClusterKubeconfig)

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
		ctx,
		infraRestConfig,
		infraClusterLabelsMap,
		*infraClusterNamespace,
		tenantClientSet,
		tenantSnapshotClientSet,
		storageClassEnforcement,
		*volumePrefix,
	)
	if err != nil {
		klog.Fatal(err)
	}

	return driver.
		WithControllerService(
			virtClient,
			*infraClusterNamespace,
			infraClusterLabelsMap,
			storageClassEnforcement,
		).
		WithIdentityService(
			identityClientset,
		)
}

// configureNodeService will prepare required clients and configuration for a
// KubeVirtCSIDriver with node service.
//
// Parameters:
//   - driver: A pointer to the KubeVirtCSIDriver to apply the configuration to.
//
// Returns:
//   - KubeVirtCSIDriver: A pointer to the passed driver.
func configureNodeService(ctx context.Context, driver *service.KubevirtCSIDriver) *service.KubevirtCSIDriver {
	var err error

	// Generate tenant clientset.
	tenantRestConfig := getConfigOrInCluster(tenantClusterKubeconfig)
	tenantClientSet, err := kubernetes.NewForConfig(tenantRestConfig)
	if err != nil {
		klog.Fatalf("Failed to build tenant client set: %v", err)
	}

	// Get node ID.
	node, err := tenantClientSet.CoreV1().Nodes().Get(ctx, *nodeName, v1.GetOptions{})
	if err != nil {
		klog.Fatalf("failed to find node by name %v: %v", *nodeName, err)
	}
	if node.Spec.ProviderID == "" {
		klog.Fatal("provider name missing from node, something's not right")
	}
	vmName := strings.TrimPrefix(node.Spec.ProviderID, `kubevirt://`)
	vmNamespace, ok := node.Annotations["cluster.x-k8s.io/cluster-namespace"]
	if !ok {
		klog.Fatal("cannot infer infra vm namespace")
	}
	nodeID := fmt.Sprintf("%s/%s", vmNamespace, vmName)
	klog.Infof("Node name: %v, Node ID: %s", *nodeName, nodeID)

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

func parseLabels() map[string]string {

	infraClusterLabelsMap := map[string]string{}

	if *infraClusterLabels == "" {
		return infraClusterLabelsMap
	}

	labelStrings := strings.Split(*infraClusterLabels, ",")

	for _, label := range labelStrings {

		labelPair := strings.SplitN(label, "=", 2)

		if len(labelPair) != 2 {
			panic("Bad labels format. Should be 'key=value,key=value,...'")
		}

		infraClusterLabelsMap[labelPair[0]] = labelPair[1]
	}

	return infraClusterLabelsMap
}

// getConfigOrInCluster will return an in cluster config if the passed
// Kubeconfig is empty. Otherwise, it will build a config from it.
//
// Parameters:
//   - kubeconfig: Path to kubeconfig file. May be empty.
//
// Returns:
//   - restConfig: Config generated from the Kubeconfig or in cluster.
func getConfigOrInCluster(kubeconfig *string) *rest.Config {
	if *kubeconfig == "" {
		// Fallback to in cluster config.
		inClusterConfig, err := rest.InClusterConfig()
		if err != nil {
			klog.Fatalf("Failed to build in cluster config: %v", err)
		}
		return inClusterConfig
	}

	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		klog.Fatalf("failed to build cluster config: %v", err)
	}
	return config
}
