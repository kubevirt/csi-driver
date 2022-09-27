package service

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	kubevirtv1 "kubevirt.io/api/core/v1"

	"github.com/container-storage-interface/spec/lib/go/csi"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	klog "k8s.io/klog/v2"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	client "kubevirt.io/csi-driver/pkg/kubevirt"
)

const (
	infraStorageClassNameParameter = "infraStorageClassName"
	busParameter                   = "bus"
	busDefaultValue                = kubevirtv1.DiskBus("scsi")
	serialParameter                = "serial"
)

//ControllerService implements the controller interface. See README for details.
type ControllerService struct {
	virtClient            client.Client
	infraClusterNamespace string
	infraClusterLabels    map[string]string
}

var controllerCaps = []csi.ControllerServiceCapability_RPC_Type{
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME, // attach/detach
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
	volumeMode := getVolumeModeFromRequest(req)
	if volumeMode == corev1.PersistentVolumeBlock {
		return nil, status.Error(codes.InvalidArgument, "block mode not supported")
	}
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
	dv.Spec.PVC = &corev1.PersistentVolumeClaimSpec{
		AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		StorageClassName: &storageClassName,
		VolumeMode:       &volumeMode,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceStorage: *resource.NewScaledQuantity(storageSize, 0)},
		},
	}
	dv.Spec.Source = &cdiv1.DataVolumeSource{}
	dv.Spec.Source.Blank = &cdiv1.DataVolumeBlankImage{}

	if existingDv, err := c.virtClient.GetDataVolume(c.infraClusterNamespace, dvName); errors.IsNotFound(err) {
		// Create DataVolume
		dv, err = c.virtClient.CreateDataVolume(c.infraClusterNamespace, dv)
		if err != nil {
			klog.Error("Failed creating DataVolume " + dvName)
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else {
		if existingDv != nil && existingDv.Spec.PVC != nil {
			// Verify capacity of original matches requested size.
			existingRequest := existingDv.Spec.PVC.Resources.Requests[corev1.ResourceStorage]
			newRequest := dv.Spec.PVC.Resources.Requests[corev1.ResourceStorage]
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
	klog.Infof("Removing data volume with %s", dvName)

	err := c.virtClient.DeleteDataVolume(c.infraClusterNamespace, dvName)
	if err != nil {
		klog.Error("Failed deleting DataVolume " + dvName)
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

	klog.Infof("Attaching DataVolume %s to Node ID %s", dvName, req.NodeId)

	// Get VM name
	vmName, err := c.getVMNameByCSINodeID(req.NodeId)
	if err != nil {
		klog.Error("Failed getting VM Name for node ID " + req.NodeId)
		return nil, err
	}

	// Determine serial number/string for the new disk
	serial := req.VolumeContext[serialParameter]

	// Determine BUS type
	bus := req.VolumeContext[busParameter]

	// hotplug DataVolume to VM
	klog.Infof("Start attaching DataVolume %s to VM %s. Volume name: %s. Serial: %s. Bus: %s", dvName, vmName, dvName, serial, bus)

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

	err = c.virtClient.AddVolumeToVM(c.infraClusterNamespace, vmName, addVolumeOptions)
	if err != nil {
		klog.Errorf("Failed adding volume %s to VM %s, %v", dvName, vmName, err)
		return nil, err
	}

	return &csi.ControllerPublishVolumeResponse{}, nil
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
	klog.Infof("Detaching DataVolume %s from Node ID %s", dvName, req.NodeId)

	// Get VM name
	vmName, err := c.getVMNameByCSINodeID(req.NodeId)
	if err != nil {
		return nil, err
	}

	// Detach DataVolume from VM
	err = c.virtClient.RemoveVolumeFromVM(c.infraClusterNamespace, vmName, &kubevirtv1.RemoveVolumeOptions{Name: dvName})
	if err != nil {
		klog.Error("Failed removing volume " + dvName + " from VM " + vmName)
		return nil, err
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

//ValidateVolumeCapabilities unimplemented
func (c *ControllerService) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume ID not provided")
	}
	if len(req.VolumeCapabilities) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "volumeCapabilities not provided for %s", req.VolumeId)
	}

	for _, cap := range req.GetVolumeCapabilities() {
		if cap.GetMount() == nil {
			return nil, status.Error(codes.InvalidArgument, "mount type is undefined")
		}
	}
	dvName := req.GetVolumeId()
	if _, err := c.virtClient.GetDataVolume(c.infraClusterNamespace, dvName); errors.IsNotFound(err) {
		return nil, status.Errorf(codes.NotFound, "volume %s not found", req.GetVolumeId())
	} else if err != nil {
		return nil, err
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeContext:      req.GetVolumeContext(),
			VolumeCapabilities: req.GetVolumeCapabilities(),
			Parameters:         req.GetParameters(),
		},
	}, nil

}

//ListVolumes unimplemented
func (c *ControllerService) ListVolumes(context.Context, *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//GetCapacity unimplemented
func (c *ControllerService) GetCapacity(context.Context, *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//CreateSnapshot unimplemented
func (c *ControllerService) CreateSnapshot(context.Context, *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//DeleteSnapshot unimplemented
func (c *ControllerService) DeleteSnapshot(context.Context, *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//ListSnapshots unimplemented
func (c *ControllerService) ListSnapshots(context.Context, *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//ControllerExpandVolume unimplemented
func (c *ControllerService) ControllerExpandVolume(context.Context, *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//ControllerGetCapabilities returns the driver's controller capabilities
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
func (c *ControllerService) getVMNameByCSINodeID(nodeID string) (string, error) {
	list, err := c.virtClient.ListVirtualMachines(c.infraClusterNamespace)
	if err != nil {
		klog.Error("Failed listing VMIs in infra cluster")
		return "", status.Error(codes.NotFound, fmt.Sprintf("failed listing VMIs in infra cluster %v", err))
	}

	for _, vmi := range list {
		if strings.EqualFold(string(vmi.Spec.Domain.Firmware.UUID), nodeID) {
			return vmi.Name, nil
		}
	}

	return "", status.Error(codes.NotFound, fmt.Sprintf("failed to find VM with domain.firmware.uuid %v", nodeID))
}

func getVolumeModeFromRequest(req *csi.CreateVolumeRequest) corev1.PersistentVolumeMode {
	volumeMode := corev1.PersistentVolumeFilesystem // Set default in case not found in request

	for _, cap := range req.VolumeCapabilities {
		if cap == nil {
			continue
		}

		if _, ok := cap.GetAccessType().(*csi.VolumeCapability_Block); ok {
			volumeMode = corev1.PersistentVolumeBlock
			break
		}
	}

	return volumeMode
}
