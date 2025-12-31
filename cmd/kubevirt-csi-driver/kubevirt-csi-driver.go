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
	handle()
	os.Exit(0)
}

func handle() {
	var tenantRestConfig *rest.Config
	var infraRestConfig *rest.Config
	var identityClientset *kubernetes.Clientset

	if service.VendorVersion == "" {
		klog.Fatal("VendorVersion must be set at compile time")
	}
	klog.V(2).Infof("Driver vendor %v %v", service.VendorName, service.VendorVersion)

	if (infraClusterLabels == nil || *infraClusterLabels == "") && !*runNodeService {
		klog.Fatal("infra-cluster-labels must be set")
	}
	if volumePrefix == nil || *volumePrefix == "" {
		klog.Fatal("volume-prefix must be set")
	}

	inClusterConfig, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Failed to build in cluster config: %v", err)
	}

	if *tenantClusterKubeconfig != "" {
		tenantRestConfig, err = clientcmd.BuildConfigFromFlags("", *tenantClusterKubeconfig)
		if err != nil {
			klog.Fatalf("failed to build tenant cluster config: %v", err)
		}
	} else {
		tenantRestConfig = inClusterConfig
	}

	if *infraClusterKubeconfig != "" {
		infraRestConfig, err = clientcmd.BuildConfigFromFlags("", *infraClusterKubeconfig)
		if err != nil {
			klog.Fatalf("failed to build infra cluster config: %v", err)
		}

	} else {
		infraRestConfig = inClusterConfig
	}

	tenantClientSet, err := kubernetes.NewForConfig(tenantRestConfig)
	if err != nil {
		klog.Fatalf("Failed to build tenant client set: %v", err)
	}
	tenantSnapshotClientSet, err := snapcli.NewForConfig(tenantRestConfig)
	if err != nil {
		klog.Fatalf("Failed to build tenant snapshot client set: %v", err)
	}

	infraClusterLabelsMap := parseLabels()
	klog.V(5).Infof("Storage class enforcement string: \n%s", infraStorageClassEnforcement)
	storageClassEnforcement := configureStorageClassEnforcement(infraStorageClassEnforcement)

	virtClient, err := kubevirt.NewClient(infraRestConfig, infraClusterLabelsMap, tenantClientSet, tenantSnapshotClientSet, storageClassEnforcement, *volumePrefix)
	if err != nil {
		klog.Fatal(err)
	}

	var nodeID string
	if *nodeName != "" {
		node, err := tenantClientSet.CoreV1().Nodes().Get(context.TODO(), *nodeName, v1.GetOptions{})
		if err != nil {
			klog.Fatal(fmt.Errorf("failed to find node by name %v: %v", nodeName, err))
		}

		nodeID, err = resolveNodeID(*nodeName, node.Spec.ProviderID, node.Annotations)
		if err != nil {
			klog.Fatal(err)
		}
		klog.Infof("Node name: %v, Node ID: %s", *nodeName, nodeID)
	}

	identityClientset = tenantClientSet
	if *runControllerService {
		identityClientset, err = kubernetes.NewForConfig(infraRestConfig)
		if err != nil {
			klog.Fatalf("Failed to build infra client set: %v", err)
		}
	}

	driver := service.NewKubevirtCSIDriver(virtClient,
		identityClientset,
		*infraClusterNamespace,
		infraClusterLabelsMap,
		storageClassEnforcement,
		nodeID,
		*runNodeService,
		*runControllerService)

	driver.Run(*endpoint)
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
