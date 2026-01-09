package service

import (
	"k8s.io/client-go/kubernetes"
	klog "k8s.io/klog/v2"

	"kubevirt.io/csi-driver/pkg/kubevirt"
	"kubevirt.io/csi-driver/pkg/util"
)

var (
	// VendorVersion is the vendor version set by ldflags at build time
	VendorVersion = "0.2.0"
	// VendorName is the CSI driver unique name, must match the storage class provisioner value.
	VendorName = "csi.kubevirt.io"
)

// KubevirtCSIDriver implements a complete CSI service
type KubevirtCSIDriver struct {
	*IdentityService
	*ControllerService
	*NodeService
	Client kubevirt.Client
}

// NewKubevirtCSIDriver returns a new unconfigured KubeVirtCSIDriver.
//
// Returns:
//   - KubevirtCSIDriver: Pointer to itself.
func NewKubevirtCSIDriver() *KubevirtCSIDriver {
	return &KubevirtCSIDriver{}
}

// WithIdentityService will configure the Identity Service of the KubeVirtCSIDriver
// with the provided clientset and provisioner name.
//
// Parameters:
//   - identityClientset: Clientset we will connect to.
//
// Returns:
//   - KubevirtCSIDriver: Pointer to itself.
func (d *KubevirtCSIDriver) WithIdentityService(
	identityClientset kubernetes.Interface,
) *KubevirtCSIDriver {
	d.IdentityService = NewIdentityService(identityClientset)
	return d
}

// WithControllerService will create a ControllerService and store it in the
// KubeVirtCSIDriver with the provided parameters.
//
// Parameters:
//   - virtClient: KubeVirt client to interact with the Infra cluster
//   - infraClusterNamespace: Namespace in the Infra cluster where the
//     KubeVirtCSIDriver will create and manage DataVolumes, VirtualMachines
//     and VirtualMachineInstances.
//   - infraClusterLabels: Labels to add and filter to Infra resources.
//   - storageClassEnforcement: Storage and Volume Snapshot classes enabled.
//
// Returns:
//   - KubevirtCSIDriver: Pointer to itself.
func (d *KubevirtCSIDriver) WithControllerService(
	virtClient kubevirt.Client,
	infraClusterNamespace string,
	infraClusterLabels map[string]string,
	storageClassEnforcement util.StorageClassEnforcement,
) *KubevirtCSIDriver {
	d.ControllerService = &ControllerService{
		virtClient:              virtClient,
		infraClusterNamespace:   infraClusterNamespace,
		infraClusterLabels:      infraClusterLabels,
		storageClassEnforcement: storageClassEnforcement,
	}
	return d
}

// WithNodeService will create a NodeService targetting the provided node.
//
// Parameters:
//   - nodeID: Name of the node
//
// Returns:
//   - KubevirtCSIDriver: Pointer to itself.
func (d *KubevirtCSIDriver) WithNodeService(
	nodeID string,
) *KubevirtCSIDriver {
	d.NodeService = NewNodeService(nodeID)
	return d
}

// Run will initiate the grpc services Identity, Controller, and Node.
func (driver *KubevirtCSIDriver) Run(endpoint string) {
	// run the gRPC server
	klog.Info("Setting the rpc server")

	s := NewNonBlockingGRPCServer()
	s.Start(endpoint, driver.IdentityService, driver.ControllerService, driver.NodeService)
	s.Wait()
}
