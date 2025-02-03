package service

import (
	"k8s.io/client-go/kubernetes"
	klog "k8s.io/klog/v2"

	"kubevirt.io/csi-driver/pkg/kubevirt"
	"kubevirt.io/csi-driver/pkg/util"
)

const (
	WellKnownRegionTopologyKey = "topology.kubernetes.io/region"
	WellKnownZoneTopologyKey   = "topology.kubernetes.io/zone"
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

func NewKubevirtCSIDriver(virtClient kubevirt.Client,
	identityClientset kubernetes.Interface,
	infraClusterNamespace string,
	infraClusterLabels map[string]string,
	storageClassEnforcement util.StorageClassEnforcement,
	nodeID string,
	runNodeService bool,
	runControllerService bool,
	allowedTopologies map[string]string) *KubevirtCSIDriver {
	d := KubevirtCSIDriver{
		IdentityService: NewIdentityService(identityClientset),
	}

	if runControllerService {
		d.ControllerService = &ControllerService{
			virtClient:              virtClient,
			infraClusterNamespace:   infraClusterNamespace,
			infraClusterLabels:      infraClusterLabels,
			storageClassEnforcement: storageClassEnforcement,
		}
	}

	if runNodeService {
		d.NodeService = NewNodeService(nodeID, allowedTopologies)
	}

	return &d
}

// Run will initiate the grpc services Identity, Controller, and Node.
func (driver *KubevirtCSIDriver) Run(endpoint string) {
	// run the gRPC server
	klog.Info("Setting the rpc server")

	s := NewNonBlockingGRPCServer()
	s.Start(endpoint, driver.IdentityService, driver.ControllerService, driver.NodeService)
	s.Wait()
}
