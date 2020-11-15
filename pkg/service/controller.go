package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kubevirt/csi-driver/pkg/kubevirt"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog"
	v1 "kubevirt.io/client-go/api/v1"
	cdiv1 "kubevirt.io/containerized-data-importer/pkg/apis/core/v1alpha1"
)

const (
	ParameterThinProvisioning      = "thinProvisioning"
	infraStorageClassNameParameter = "infraStorageClassName"
	busParameter                   = "bus"
	serialContextParameter         = "serial"
)

//ControllerService implements the controller interface
type ControllerService struct {
	infraClusterClient    kubernetes.Clientset
	kubevirtClient        kubevirt.Client
	infraClusterNamespace string
}

var ControllerCaps = []csi.ControllerServiceCapability_RPC_Type{
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME, // attach/detach
}

//CreateVolume creates the disk for the request, unattached from any VM
func (c *ControllerService) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	klog.Infof("Creating disk %s", req.Name)

	// Create DataVolume object
	// Create DataVolume resource in infra cluster
	// Get details of new DataVolume resource
	// Wait until DataVolume is ready??

	storageClassName := req.Parameters[infraStorageClassNameParameter]
	volumeMode := corev1.PersistentVolumeFilesystem // TODO: get it from req.VolumeCapabilities
	quantity := resource.NewScaledQuantity(req.GetCapacityRange().GetRequiredBytes(), 0)
	klog.Infof("quantity is %v", quantity)

	dv := cdiv1.DataVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: req.Name,
		},
		Spec: cdiv1.DataVolumeSpec{
			Source: cdiv1.DataVolumeSource{
				Blank: &cdiv1.DataVolumeBlankImage{},
			},
			PVC: &corev1.PersistentVolumeClaimSpec{
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				StorageClassName: &storageClassName,
				VolumeMode:       &volumeMode,
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: *quantity},
				},
			},
		},
	}

	bus := req.Parameters[busParameter]
	// idempotence - try to create and check if exists already
	err := c.kubevirtClient.CreateDataVolume(c.infraClusterNamespace, dv)
	if err != nil && !errors.IsAlreadyExists(err) {
		klog.Errorf("failed t o create data volume on infra-cluster %v", err)
		return nil, err
	}
	dataVolume, err := c.kubevirtClient.GetDataVolume(c.infraClusterNamespace, dv.Name)
	if err != nil {
		klog.Errorf("failed to fetch data volume '%s' on infra-cluster %v", dv.Name, err)
		return nil, err
	}

	// TODO support for thin/thick provisioning from the storage class parameters
	_, _ = strconv.ParseBool(req.Parameters[ParameterThinProvisioning])

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			CapacityBytes: req.GetCapacityRange().GetRequiredBytes(),
			VolumeId:      dataVolume.Name,
			VolumeContext: map[string]string{
				busParameter:           bus,
				serialContextParameter: string(dataVolume.UID),
			},
		},
	}, nil
}

//DeleteVolume removed the data volume from kubevirt
func (c *ControllerService) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	klog.Infof("Removing data volume with ID %s", req.VolumeId)

	// Yaron since we set the VolumeID in CreateVolume then for use the volumeID==dvName,
	// so we don't need the lines here
	//dvName, err := c.getDataVolumeNameByUID(ctx, req.VolumeId)
	//if err != nil {
	//	return nil, err
	//}

	err := c.kubevirtClient.DeleteDataVolume(c.infraClusterNamespace, req.VolumeId)
	return &csi.DeleteVolumeResponse{}, err
}

// ControllerPublishVolume takes a volume, which is an kubevirt disk, and attaches it to a node, which is an kubevirt VM.
func (c *ControllerService) ControllerPublishVolume(
	ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {

	// req.NodeId == kubevirt VM name
	klog.Infof("Attaching DataVolume UID %s to Node ID %s", req.VolumeId, req.NodeId)

	dv, err := c.kubevirtClient.GetDataVolume(c.infraClusterNamespace, req.VolumeId)
	if err != nil {
		return nil, err
	}

	// Get VM name
	vmName, err := c.getVmNameByCSINodeID(ctx, c.infraClusterNamespace, req.NodeId)
	if err != nil {
		return nil, err
	}

	// Determine disk name (disk-<DataVolume-name>)
	diskName := "disk-" + dv.Name

	// Determine BUS type
	bus := req.VolumeContext[busParameter]

	dvName := dv.Name
	serial := string(dv.UID)

	// hotplug DataVolume to VM
	klog.Infof("Start attaching DataVolume %s to VM %s. Disk name: %s. Serial: %s. Bus: %s", dvName, vmName, diskName, serial, bus)

	hotplugRequest := v1.HotplugVolumeRequest{
		Volume: &v1.Volume{
			VolumeSource: v1.VolumeSource{
				DataVolume: &v1.DataVolumeSource{
					Name: dvName,
				},
			},
			Name: diskName,
		},
		Disk: &v1.Disk{
			Name:   diskName,
			Serial: serial,
			DiskDevice: v1.DiskDevice{
				Disk: &v1.DiskTarget{
					Bus: bus,
				},
			},
		},
		Ephemeral: false,
	}
	err = c.kubevirtClient.AddVolumeToVM(c.infraClusterNamespace, vmName, hotplugRequest)
	if err != nil {
		return nil, err
	}

	return &csi.ControllerPublishVolumeResponse{}, nil
}

//ControllerUnpublishVolume detaches the disk from the VM.
func (c *ControllerService) ControllerUnpublishVolume(_ context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	// req.NodeId == kubevirt VM name
	klog.Infof("Detaching DataVolume UID %s from Node ID %s", req.VolumeId, req.NodeId)

	dv, err := c.kubevirtClient.GetDataVolume(c.infraClusterNamespace, req.VolumeId)
	if err != nil {
		return nil, err
	}

	// Get VM name
	vmName, err := c.getVmNameByCSINodeID(context.Background(), c.infraClusterNamespace, req.NodeId)
	if err != nil {
		return nil, err
	}

	// Determine disk name (disk-<DataVolume-name>)
	diskName := "disk-" + dv.Name

	// Detach DataVolume from VM
	hotplugRequest := v1.HotplugVolumeRequest{
		Volume: &v1.Volume{
			VolumeSource: v1.VolumeSource{
				DataVolume: &v1.DataVolumeSource{
					Name: dv.Name,
				},
			},
			Name: diskName,
		},
	}
	err = c.kubevirtClient.RemoveVolumeFromVM(c.infraClusterNamespace, vmName, hotplugRequest)
	if err != nil {
		return nil, err
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

//ValidateVolumeCapabilities
func (c *ControllerService) ValidateVolumeCapabilities(context.Context, *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//ListVolumes
func (c *ControllerService) ListVolumes(context.Context, *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//GetCapacity
func (c *ControllerService) GetCapacity(context.Context, *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//CreateSnapshot
func (c *ControllerService) CreateSnapshot(context.Context, *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//DeleteSnapshot
func (c *ControllerService) DeleteSnapshot(context.Context, *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//ListSnapshots
func (c *ControllerService) ListSnapshots(context.Context, *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//ControllerExpandVolume
func (c *ControllerService) ControllerExpandVolume(context.Context, *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//ControllerGetCapabilities
func (c *ControllerService) ControllerGetCapabilities(context.Context, *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	caps := make([]*csi.ControllerServiceCapability, 0, len(ControllerCaps))
	for _, capability := range ControllerCaps {
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

func (c *ControllerService) ControllerGetVolume(ctx context.Context, request *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {

	return &csi.ControllerGetVolumeResponse{
		Volume: &csi.Volume{
			CapacityBytes: 0,
			VolumeId:      "TODO",
		},
	}, nil
}

func (c *ControllerService) getDataVolumeNameByUID(ctx context.Context, uid string) (string, error) {
	dvs, err := c.kubevirtClient.ListDataVolumes(c.infraClusterNamespace)
	if err != nil {
		return "", err
	}
	for _, dv := range dvs {
		if string(dv.GetUID()) == uid {
			return dv.Name, nil
		}
	}

	return "", fmt.Errorf("failed to match DataVolume by uid %s", uid)
}

// getVmNameByCSINodeID find a VM in infra cluster by its firmware uuid. The uid is the ID that the CSI node
// part publishes in NodeGetInfo and then used by CSINode.spec.drivers[].nodeID
func (c *ControllerService) getVmNameByCSINodeID(_ context.Context, namespace string, csiNodeID string) (string, error) {
	vmis, err := c.kubevirtClient.ListVirtualMachines(namespace)
	if err != nil {
		klog.Errorf("failed to list VMIS %v", err)
		return "", err
	}

	for _, vmi := range vmis {
		if string(vmi.Spec.Domain.Firmware.UUID) == strings.ToLower(csiNodeID) {
			return vmi.Name, nil
		}
	}
	return "", fmt.Errorf("failed to find VM with domain.firmware.uuid %v", csiNodeID)
}
