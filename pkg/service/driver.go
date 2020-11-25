package service

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	"github.com/kubevirt/csi-driver/pkg/kubevirt"
)

var (
	// VendorVersion is the vendor version set by ldflags at build time
	VendorVersion = "0.1.0"
	// VendorName is the CSI driver unique name, must match the storage class provisioner value.
	VendorName    = "csi.kubevirt.io"
)

// KubevirtCSIDriver implements a complete CSI service
type KubevirtCSIDriver struct {
	*IdentityService
	*ControllerService
	*NodeService
	infraClusterClient kubernetes.Clientset
	Client             kubevirt.Client
}

func NewKubevirtCSIDriver(infraClusterClient kubevirt.Client, infraClusterNamespace string, nodeID string) *KubevirtCSIDriver {
	d := KubevirtCSIDriver{
		IdentityService: &IdentityService{
			infraClusterClient: infraClusterClient,
		},
		ControllerService: &ControllerService{
			infraClusterNamespace: infraClusterNamespace,
			infraClient:    infraClusterClient,
		},
		NodeService: &NodeService{
			infraClusterClient: kubernetes.Clientset{},
			kubevirtClient:     infraClusterClient,
			nodeID:             nodeID,
		},
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
