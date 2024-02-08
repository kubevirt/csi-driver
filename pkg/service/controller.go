package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	client "kubevirt.io/csi-driver/pkg/kubevirt"
	"kubevirt.io/csi-driver/pkg/util"
)

const (
	infraStorageClassNameParameter = "infraStorageClassName"
	busParameter                   = "bus"
	busDefaultValue                = kubevirtv1.DiskBus("scsi")
	serialParameter                = "serial"
)

var (
	unallowedStorageClass = status.Error(codes.InvalidArgument, "infraStorageclass is not in the allowed list")
)

// ControllerService implements the controller interface. See README for details.
type ControllerService struct {
	virtClient              client.Client
	infraClusterNamespace   string
	infraClusterLabels      map[string]string
	storageClassEnforcement util.StorageClassEnforcement
}

var controllerCaps = []csi.ControllerServiceCapability_RPC_Type{
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME, // attach/detach
}

// Contains tells whether a contains x.
func contains(arr []string, val string) bool {
	for _, itrVal := range arr {
		if val == itrVal {
			return true
		}
	}
	return false
}

func (c *ControllerService) validateCreateVolumeRequest(req *csi.CreateVolumeRequest) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "missing request")
	}
	// Check arguments
	if len(req.GetName()) == 0 {
		return status.Error(codes.InvalidArgument, "name missing in request")
	}
	caps := req.GetVolumeCapabilities()
	if caps == nil {
		return status.Error(codes.InvalidArgument, "volume capabilities missing in request")
	}

	if c.storageClassEnforcement.AllowAll {
		return nil
	}

	storageClassName := req.Parameters[infraStorageClassNameParameter]
	if storageClassName == "" {
		if c.storageClassEnforcement.AllowDefault {
			return nil
		} else {
			return unallowedStorageClass
		}
	}
	if !contains(c.storageClassEnforcement.AllowList, storageClassName) {
		return unallowedStorageClass
	}

	return nil
}

// CreateVolume Create a new DataVolume.
// The new DataVolume.Name is csi.Volume.VolumeID.
// The new DataVolume.ID is used as the disk serial.
func (c *ControllerService) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if req != nil {
		klog.V(3).Infof("Create Volume Request: %+v", *req)
	}
	if err := c.validateCreateVolumeRequest(req); err != nil {
		return nil, err
	}

	// Prepare parameters for the DataVolume
	storageClassName := req.Parameters[infraStorageClassNameParameter]
	storageSize := req.GetCapacityRange().GetRequiredBytes()
	dvName := req.Name
	value, ok := req.Parameters[busParameter]
	var bus kubevirtv1.DiskBus
	if ok {
		bus = kubevirtv1.DiskBus(value)
	} else {
		bus = busDefaultValue
	}

	// Create DataVolume object
	dv := &cdiv1.DataVolume{}
	dv.Name = dvName
	dv.Namespace = c.infraClusterNamespace
	dv.Kind = "DataVolume"
	dv.APIVersion = cdiv1.SchemeGroupVersion.String()
	dv.ObjectMeta.Labels = c.infraClusterLabels
	dv.ObjectMeta.Annotations = map[string]string{
		"cdi.kubevirt.io/storage.deleteAfterCompletion": "false",
	}
	dv.Spec.Storage = &cdiv1.StorageSpec{
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: *resource.NewScaledQuantity(storageSize, 0)},
		},
	}

	// Only set the storageclass if it is defined. Otherwise we use the
	// default storage class which means leaving the storageclass empty
	// (nil) on the PVC
	if storageClassName != "" {
		dv.Spec.Storage.StorageClassName = &storageClassName
	}

	dv.Spec.Source = &cdiv1.DataVolumeSource{}
	dv.Spec.Source.Blank = &cdiv1.DataVolumeBlankImage{}

	if existingDv, err := c.virtClient.GetDataVolume(ctx, c.infraClusterNamespace, dvName); errors.IsNotFound(err) {
		// Create DataVolume
		dv, err = c.virtClient.CreateDataVolume(ctx, c.infraClusterNamespace, dv)
		if err != nil {
			klog.Error("failed creating DataVolume " + dvName)
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else {
		if existingDv != nil && existingDv.Spec.Storage != nil {
			// Verify capacity of original matches requested size.
			existingRequest := existingDv.Spec.Storage.Resources.Requests[corev1.ResourceStorage]
			newRequest := dv.Spec.Storage.Resources.Requests[corev1.ResourceStorage]
			if newRequest.Cmp(existingRequest) != 0 {
				return nil, status.Error(codes.AlreadyExists, "Requested storage size does not match existing size")
			}
			dv = existingDv
		}
	}

	// Prepare serial for disk
	serial := string(dv.GetUID())

	// Return response
	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			CapacityBytes: storageSize,
			VolumeId:      dvName,
			VolumeContext: map[string]string{
				busParameter:    string(bus),
				serialParameter: serial,
			},
		},
	}, nil
}

func (c *ControllerService) validateDeleteVolumeRequest(req *csi.DeleteVolumeRequest) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "missing request")
	}
	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	return nil
}

// DeleteVolume removes the data volume from kubevirt
func (c *ControllerService) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if req != nil {
		klog.V(3).Infof("Delete Volume Request: %+v", *req)
	}
	if err := c.validateDeleteVolumeRequest(req); err != nil {
		return nil, err
	}
	dvName := req.VolumeId
	klog.V(3).Infof("Removing data volume with %s", dvName)

	err := c.virtClient.DeleteDataVolume(ctx, c.infraClusterNamespace, dvName)
	if err != nil {
		klog.Error("failed deleting DataVolume " + dvName)
		return nil, err
	}

	return &csi.DeleteVolumeResponse{}, nil
}

func (c *ControllerService) validateControllerPublishVolumeRequest(req *csi.ControllerPublishVolumeRequest) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "missing request")
	}
	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return status.Error(codes.InvalidArgument, "volume id missing in request")
	}
	if len(req.GetNodeId()) == 0 {
		return status.Error(codes.InvalidArgument, "node id missing in request")
	}
	if req.GetVolumeCapability() == nil {
		return status.Error(codes.InvalidArgument, "volume capability missing in request")
	}
	return nil
}

// ControllerPublishVolume takes a volume, which is an kubevirt disk, and attaches it to a node, which is an kubevirt VM.
func (c *ControllerService) ControllerPublishVolume(
	ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	if err := c.validateControllerPublishVolumeRequest(req); err != nil {
		return nil, err
	}
	dvName := req.GetVolumeId()

	klog.V(3).Infof("Attaching DataVolume %s to Node ID %s", dvName, req.NodeId)

	// Get VM name
	vmName, err := c.getVMNameByCSINodeID(ctx, req.NodeId)
	if err != nil {
		klog.Error("failed getting VM Name for node ID " + req.NodeId)
		return nil, err
	}

	// Determine serial number/string for the new disk
	serial := req.VolumeContext[serialParameter]

	// Determine BUS type
	bus := req.VolumeContext[busParameter]

	// hotplug DataVolume to VM
	klog.V(3).Infof("Start attaching DataVolume %s to VM %s. Volume name: %s. Serial: %s. Bus: %s", dvName, vmName, dvName, serial, bus)

	addVolumeOptions := &kubevirtv1.AddVolumeOptions{
		Name: dvName,
		Disk: &kubevirtv1.Disk{
			Serial: serial,
			DiskDevice: kubevirtv1.DiskDevice{
				Disk: &kubevirtv1.DiskTarget{
					Bus: kubevirtv1.DiskBus(bus),
				},
			},
		},
		VolumeSource: &kubevirtv1.HotplugVolumeSource{
			DataVolume: &kubevirtv1.DataVolumeSource{
				Name: dvName,
			},
		},
	}

	if err := wait.ExponentialBackoff(wait.Backoff{
		Duration: time.Second,
		Steps:    5,
		Factor:   2,
		Cap:      time.Second * 30,
	}, func() (bool, error) {
		if err := c.addVolumeToVm(ctx, dvName, vmName, addVolumeOptions); err != nil {
			klog.Infof("failed adding volume %s to VM %s, retrying, err: %v", dvName, vmName, err)
			return false, nil
		}
		return true, nil
	}); err != nil {
		return nil, err
	}

	// Ensure that the csi-attacher and csi-provisioner --timeout values are > the timeout specified here so we don't get
	// odd failures with detaching volumes.
	err = c.virtClient.EnsureVolumeAvailable(ctx, c.infraClusterNamespace, vmName, dvName, time.Minute*2)
	if err != nil {
		klog.Errorf("volume %s failed to be ready in time (2m) in VM %s, %v", dvName, vmName, err)
		return nil, err
	}

	klog.V(3).Infof("Successfully attached volume %s to VM %s", dvName, vmName)
	return &csi.ControllerPublishVolumeResponse{}, nil
}

func (c *ControllerService) isVolumeAttached(ctx context.Context, dvName, vmName string) (bool, error) {
	vm, err := c.virtClient.GetVirtualMachine(ctx, c.infraClusterNamespace, vmName)
	if err != nil {
		return false, err
	}
	for _, volumeStatus := range vm.Status.VolumeStatus {
		if volumeStatus.Name == dvName {
			return true, nil
		}
	}
	return false, nil
}

func (c *ControllerService) addVolumeToVm(ctx context.Context, dvName, vmName string, addVolumeOptions *kubevirtv1.AddVolumeOptions) error {
	volumeFound, err := c.isVolumeAttached(ctx, dvName, vmName)
	if err != nil {
		return err
	}
	if !volumeFound {
		err = c.virtClient.AddVolumeToVM(ctx, c.infraClusterNamespace, vmName, addVolumeOptions)
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *ControllerService) validateControllerUnpublishVolumeRequest(req *csi.ControllerUnpublishVolumeRequest) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "missing request")
	}
	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return status.Error(codes.InvalidArgument, "volume id missing in request")
	}
	if len(req.GetNodeId()) == 0 {
		return status.Error(codes.InvalidArgument, "node id missing in request")
	}
	return nil
}

// ControllerUnpublishVolume detaches the disk from the VM.
func (c *ControllerService) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	if err := c.validateControllerUnpublishVolumeRequest(req); err != nil {
		return nil, err
	}
	dvName := req.VolumeId
	klog.V(3).Infof("Detaching DataVolume %s from Node ID %s", dvName, req.NodeId)

	// Get VM name
	vmName, err := c.getVMNameByCSINodeID(ctx, req.NodeId)
	if err != nil {
		return nil, err
	}

	if err := wait.ExponentialBackoff(wait.Backoff{
		Duration: time.Second,
		Steps:    5,
		Factor:   2,
		Cap:      time.Second * 30,
	}, func() (bool, error) {
		if err := c.removeVolumeFromVm(ctx, dvName, vmName); err != nil {
			klog.Infof("failed removing volume %s from VM %s, err: %v", dvName, vmName, err)
			return false, nil
		}
		return true, nil
	}); err != nil {
		return nil, err
	}

	err = c.virtClient.EnsureVolumeRemoved(ctx, c.infraClusterNamespace, vmName, dvName, time.Minute*2)
	if err != nil {
		klog.Errorf("volume %s failed to be removed in time (2m) from VM %s, %v", dvName, vmName, err)
		return nil, err
	}

	klog.V(3).Infof("Successfully unpublished volume %s from VM %s", dvName, vmName)
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (c *ControllerService) removeVolumeFromVm(ctx context.Context, dvName, vmName string) error {
	vm, err := c.virtClient.GetVirtualMachine(ctx, c.infraClusterNamespace, vmName)
	if err != nil {
		return err
	}
	removePossible := false
	for _, volumeStatus := range vm.Status.VolumeStatus {
		if volumeStatus.HotplugVolume != nil && volumeStatus.Name == dvName {
			removePossible = true
		}
	}
	if removePossible {
		// Detach DataVolume from VM
		err = c.virtClient.RemoveVolumeFromVM(ctx, c.infraClusterNamespace, vmName, &kubevirtv1.RemoveVolumeOptions{Name: dvName})
		if err != nil {
			return err
		}
	}
	return nil
}

// ValidateVolumeCapabilities unimplemented
func (c *ControllerService) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume ID not provided")
	}
	if len(req.VolumeCapabilities) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "volumeCapabilities not provided for %s", req.VolumeId)
	}
	klog.V(3).Info("Calling volume capabilities")
	for _, capability := range req.GetVolumeCapabilities() {
		if capability.GetMount() == nil {
			return nil, status.Error(codes.InvalidArgument, "mount type is undefined")
		}
	}
	dvName := req.GetVolumeId()
	klog.V(3).Infof("DataVolume name %s", dvName)
	if _, err := c.virtClient.GetDataVolume(ctx, c.infraClusterNamespace, dvName); errors.IsNotFound(err) {
		return nil, status.Errorf(codes.NotFound, "volume %s not found", req.GetVolumeId())
	} else if err != nil {
		return nil, err
	}

	klog.V(5).Infof("Returning capabilities %v", &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeContext:      req.GetVolumeContext(),
			VolumeCapabilities: req.GetVolumeCapabilities(),
			Parameters:         req.GetParameters(),
		},
	})
	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeContext:      req.GetVolumeContext(),
			VolumeCapabilities: req.GetVolumeCapabilities(),
			Parameters:         req.GetParameters(),
		},
	}, nil

}

// ListVolumes unimplemented
func (c *ControllerService) ListVolumes(context.Context, *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// GetCapacity unimplemented
func (c *ControllerService) GetCapacity(context.Context, *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// CreateSnapshot unimplemented
func (c *ControllerService) CreateSnapshot(context.Context, *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// DeleteSnapshot unimplemented
func (c *ControllerService) DeleteSnapshot(context.Context, *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// ListSnapshots unimplemented
func (c *ControllerService) ListSnapshots(context.Context, *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// ControllerExpandVolume unimplemented
func (c *ControllerService) ControllerExpandVolume(context.Context, *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// ControllerGetCapabilities returns the driver's controller capabilities
func (c *ControllerService) ControllerGetCapabilities(context.Context, *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	caps := make([]*csi.ControllerServiceCapability, 0, len(controllerCaps))
	for _, capability := range controllerCaps {
		caps = append(
			caps,
			&csi.ControllerServiceCapability{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: capability,
					},
				},
			},
		)
	}
	return &csi.ControllerGetCapabilitiesResponse{Capabilities: caps}, nil
}

// ControllerGetVolume unimplemented
func (c *ControllerService) ControllerGetVolume(_ context.Context, _ *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// getVMNameByCSINodeID finds a VM in infra cluster by its firmware uuid. The uid is the ID that the CSI
// node publishes in NodeGetInfo and then used by CSINode.spec.drivers[].nodeID
func (c *ControllerService) getVMNameByCSINodeID(ctx context.Context, nodeID string) (string, error) {
	list, err := c.virtClient.ListVirtualMachines(ctx, c.infraClusterNamespace)
	if err != nil {
		klog.Error("failed listing VMIs in infra cluster")
		return "", status.Error(codes.NotFound, fmt.Sprintf("failed listing VMIs in infra cluster %v", err))
	}

	for _, vmi := range list {
		if strings.EqualFold(string(vmi.Spec.Domain.Firmware.UUID), nodeID) {
			return vmi.Name, nil
		}
	}

	return "", status.Error(codes.NotFound, fmt.Sprintf("failed to find VM with domain.firmware.uuid %v", nodeID))
}
