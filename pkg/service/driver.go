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
func NewkubevirtCSIDriver(internalInfraClient kubevirt.Client, nodeId string, infraClusterNamespace string) *kubevirtCSIDriver {
	d := kubevirtCSIDriver{
		IdentityService: &IdentityService{
			infraClusterClient: internalInfraClient,
		},
		ControllerService: &ControllerService{
			infraClusterNamespace: infraClusterNamespace,
			infraClient:    internalInfraClient,
		},
		NodeService: &NodeService{
			infraClusterClient: kubernetes.Clientset{},
			kubevirtClient:     internalInfraClient,
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
