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
	VendorName = "csi.kubevirt.io"
)

// KubevirtCSIDriver implements a complete CSI service
type KubevirtCSIDriver struct {
	*IdentityService
	*ControllerService
	*NodeService
	infraClusterClient kubernetes.Clientset
	Client             kubevirt.Client
}

func NewKubevirtCSIDriver(infraClusterClient kubevirt.Client, infraClusterNamespace string, infraClusterLabels map[string]string, nodeID string, runNodeService bool, runControllerService bool) *KubevirtCSIDriver {
	var nodeService *NodeService
	var controllerService *ControllerService

	if (runNodeService) {
		nodeService = NewNodeService(infraClusterClient, nodeID) 
	} else {
		nodeService = nil
	}
	
	if(runControllerService) {
		controllerService = &ControllerService{
			infraClient:           infraClusterClient,
			infraClusterNamespace: infraClusterNamespace,
			infraClusterLabels:    infraClusterLabels,
		}
	} else {
		controllerService = nil
	}

	d := KubevirtCSIDriver{
		IdentityService: &IdentityService{
			infraClusterClient: infraClusterClient,
		},
		ControllerService: controllerService,
		NodeService: nodeService,
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
