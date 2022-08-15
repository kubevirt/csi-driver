package service

import (
	klog "k8s.io/klog/v2"

	"kubevirt.io/csi-driver/pkg/kubevirt"
)

var (
	// VendorVersion is the vendor version set by ldflags at build time
	VendorVersion = "0.1.0"
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

func NewKubevirtCSIDriver(infraClusterClient kubevirt.Client, infraClusterNamespace string, infraClusterLabels map[string]string, nodeID string) *KubevirtCSIDriver {
	d := KubevirtCSIDriver{
		IdentityService: &IdentityService{
			infraClusterClient: infraClusterClient,
		},
		ControllerService: &ControllerService{
			infraClient:           infraClusterClient,
			infraClusterNamespace: infraClusterNamespace,
			infraClusterLabels:    infraClusterLabels,
		},
		NodeService: NewNodeService(nodeID),
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
