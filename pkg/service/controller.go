package service

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	v1 "kubevirt.io/client-go/api/v1"

	"github.com/container-storage-interface/spec/lib/go/csi"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	log "k8s.io/klog"
	cdiv1 "kubevirt.io/containerized-data-importer/pkg/apis/core/v1alpha1"

	client "github.com/kubevirt/csi-driver/pkg/kubevirt"
)

const (
	infraStorageClassNameParameter = "infraStorageClassName"
	busParameter                   = "bus"
	busDefaultValue                = "scsi"
	serialParameter                = "serial"
	hotplugDiskPrefix              = "disk-"
)

//ControllerService implements the controller interface. See README for details.
type ControllerService struct {
	infraClient           client.Client
	infraClusterNamespace string
	infraClusterLabels    map[string]string
}

var controllerCaps = []csi.ControllerServiceCapability_RPC_Type{
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME, // attach/detach
}

// CreateVolume Create a new DataVolume.
// The new DataVolume.Name is csi.Volume.VolumeID.
// The new DataVolume.ID is used as the disk serial.
func (c *ControllerService) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	log.Infof("Creating volume %s", req.Name)

	// Prepare parameters for the DataVolume
	storageClassName := req.Parameters[infraStorageClassNameParameter]
	volumeMode := getVolumeModeFromRequest(req)
	storageSize := req.GetCapacityRange().GetRequiredBytes()
	dvName := req.Name
	bus, ok := req.Parameters[busParameter]
	if !ok {
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
	dv.Spec.Source.Blank = &cdiv1.DataVolumeBlankImage{}

	// Create DataVolume
	dv, err := c.infraClient.CreateDataVolume(c.infraClusterNamespace, dv)

	if err != nil {
		log.Error("Failed creating DataVolume " + dvName)
		return nil, err
	}

	// Prepare serial for disk
	serial := string(dv.GetUID())

	// Return response
	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			CapacityBytes: storageSize,
			VolumeId:      dvName,
			VolumeContext: map[string]string{
				busParameter:    bus,
				serialParameter: serial,
			},
		},
	}, nil
}

// DeleteVolume removes the data volume from kubevirt
func (c *ControllerService) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	dvName := req.VolumeId
	log.Infof("Removing data volume with %s", dvName)

	err := c.infraClient.DeleteDataVolume(c.infraClusterNamespace, dvName)
	if err != nil {
		log.Error("Failed deleting DataVolume " + dvName)
		return nil, err
	}

	return &csi.DeleteVolumeResponse{}, nil
}

// ControllerPublishVolume takes a volume, which is an kubevirt disk, and attaches it to a node, which is an kubevirt VM.
func (c *ControllerService) ControllerPublishVolume(
	ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {

	dvName := req.VolumeId

	log.Infof("Attaching DataVolume %s to Node ID %s", dvName, req.NodeId)

	// Get VM name
	vmName, err := c.getVMNameByCSINodeID(req.NodeId)
	if err != nil {
		log.Error("Failed getting VM Name for node ID " + req.NodeId)
		return nil, err
	}

	// Determine disk name (disk-<DataVolume-name>)
	diskName := hotplugDiskPrefix + dvName

	// Determine serial number/string for the new disk
	serial := req.VolumeContext[serialParameter]

	// Determine BUS type
	bus := req.VolumeContext[busParameter]

	// hotplug DataVolume to VM
	log.Infof("Start attaching DataVolume %s to VM %s. Disk name: %s. Serial: %s. Bus: %s", dvName, vmName, diskName, serial, bus)

	addVolumeOptions := &v1.AddVolumeOptions{
		Name: diskName,
		Disk: &v1.Disk{
			Serial: serial,
			DiskDevice: v1.DiskDevice{
				Disk: &v1.DiskTarget{
					Bus: bus,
				},
			},
		},
		VolumeSource: &v1.HotplugVolumeSource{
			DataVolume: &v1.DataVolumeSource{
				Name: dvName,
			},
		},
	}

	err = c.infraClient.AddVolumeToVM(c.infraClusterNamespace, vmName, addVolumeOptions)
	if err != nil {
		log.Error("Failed adding volume " + dvName + " to VM " + vmName)
		return nil, err
	}

	return &csi.ControllerPublishVolumeResponse{}, nil
}

// ControllerUnpublishVolume detaches the disk from the VM.
func (c *ControllerService) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	dvName := req.VolumeId
	log.Infof("Detaching DataVolume %s from Node ID %s", dvName, req.NodeId)

	// Get VM name
	vmName, err := c.getVMNameByCSINodeID(req.NodeId)
	if err != nil {
		return nil, err
	}

	// Determine disk name (disk-<DataVolume-name>)
	diskName := hotplugDiskPrefix + dvName

	// Detach DataVolume from VM
	err = c.infraClient.RemoveVolumeFromVM(c.infraClusterNamespace, vmName, &v1.RemoveVolumeOptions{Name: diskName})
	if err != nil {
		log.Error("Failed removing volume " + diskName + " from VM " + vmName)
		return nil, err
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

//ValidateVolumeCapabilities unimplemented
func (c *ControllerService) ValidateVolumeCapabilities(context.Context, *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
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
	list, err := c.infraClient.ListVirtualMachines(c.infraClusterNamespace)
	if err != nil {
		log.Error("Failed listing VMIs in infra cluster")
		return "", err
	}

	for _, vmi := range list {
		if strings.ToLower(string(vmi.Spec.Domain.Firmware.UUID)) == strings.ToLower(nodeID) {
			return vmi.Name, nil
		}
	}

	return "", fmt.Errorf("Failed to find VM with domain.firmware.uuid %v", nodeID)
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
