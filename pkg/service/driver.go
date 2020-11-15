package service

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	"github.com/kubevirt/csi-driver/pkg/kubevirt"
)

var (
	// set by ldflags
	VendorVersion = "0.1.0"
	VendorName    = "csi.kubevirt.io"
)

type kubevirtCSIDriver struct {
	*IdentityService
	*ControllerService
	*NodeService
	infraClusterClient kubernetes.Clientset
	Client             kubevirt.Client
}

// NewkubevirtCSIDriver creates a driver instance
func NewkubevirtCSIDriver(infraClusterClient kubernetes.Clientset, virtClient kubevirt.Client, infraClusterNamespace string, nodeId string) *kubevirtCSIDriver {
	d := kubevirtCSIDriver{
		IdentityService: &IdentityService{
			infraClusterClient: virtClient,
		},
		ControllerService: &ControllerService{
			infraClusterNamespace: infraClusterNamespace,
			infraClusterClient:    infraClusterClient,
			kubevirtClient:        virtClient,
		},
		NodeService: &NodeService{
			infraClusterClient: kubernetes.Clientset{},
			kubevirtClient:     virtClient,
			nodeId:             nodeId,
		},
	}
	return &d
}

// Run will initiate the grpc services Identity, Controller, and Node.
func (driver *kubevirtCSIDriver) Run(endpoint string) {
	// run the gRPC server
	klog.Info("Setting the rpc server")

	s := NewNonBlockingGRPCServer()
	s.Start(endpoint, driver.IdentityService, driver.ControllerService, driver.NodeService)
	s.Wait()
}
