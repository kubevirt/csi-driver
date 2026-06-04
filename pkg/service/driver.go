package service

import (
	"context"

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

	detachReconciler *DetachReconciler
}

// NewKubevirtCSIDriver returns a new unconfigured KubeVirtCSIDriver.
func NewKubevirtCSIDriver() *KubevirtCSIDriver {
	return &KubevirtCSIDriver{}
}

// WithIdentityService configures the Identity Service of the KubeVirtCSIDriver
// with the provided clientset and provisioner name.
func (d *KubevirtCSIDriver) WithIdentityService(
	identityClientset kubernetes.Interface,
) *KubevirtCSIDriver {
	d.IdentityService = NewIdentityService(identityClientset)
	return d
}

// WithControllerService creates a ControllerService and store it in the
// KubeVirtCSIDriver with the provided parameters.
func (d *KubevirtCSIDriver) WithControllerService(
	virtClient kubevirt.Client,
	infraClusterNamespace string,
	infraClusterLabels map[string]string,
	storageClassEnforcement util.StorageClassEnforcement,
) *KubevirtCSIDriver {
	d.ControllerService = NewControllerService(
		virtClient,
		infraClusterNamespace,
		infraClusterLabels,
		storageClassEnforcement,
	)
	return d
}

// WithNodeService creates a NodeService targeting the provided node.
func (d *KubevirtCSIDriver) WithNodeService(
	nodeID string,
) *KubevirtCSIDriver {
	d.NodeService = NewNodeService(nodeID)
	return d
}

// WithDetachReconciler attaches an opt-in background reconciler that
// periodically removes orphan hot-plug volumes from VMIs in the infra
// cluster namespace. The reconciler is only started by Run() when non-nil.
func (d *KubevirtCSIDriver) WithDetachReconciler(r *DetachReconciler) *KubevirtCSIDriver {
	d.detachReconciler = r
	return d
}

// Run will initiate the grpc services Identity, Controller, and Node, and
// start the optional detach reconciler if one was configured.
func (driver *KubevirtCSIDriver) Run(endpoint string) {
	if driver.detachReconciler != nil {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			if err := driver.detachReconciler.Run(ctx); err != nil {
				klog.Errorf("detach reconciler exited: %v", err)
			}
		}()
	}

	// run the gRPC server
	klog.Info("Setting the rpc server")

	s := NewNonBlockingGRPCServer()
	s.Start(endpoint, driver.IdentityService, driver.ControllerService, driver.NodeService)
	s.Wait()
}
