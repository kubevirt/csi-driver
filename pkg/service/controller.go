package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	client "kubevirt.io/csi-driver/pkg/kubevirt"
	"kubevirt.io/csi-driver/pkg/util"
)

const (
	busParameter    = "bus"
	busDefaultValue = kubevirtv1.DiskBus("scsi")
	serialParameter = "serial"
)

var (
	unallowedStorageClass = status.Error(codes.InvalidArgument, "infraStorageclass is not in the allowed list")
)

// ControllerService implements the controller interface. See README for details.
type ControllerService struct {
	csi.UnimplementedControllerServer
	virtClient              client.Client
	infraClusterNamespace   string
	infraClusterLabels      map[string]string
	storageClassEnforcement util.StorageClassEnforcement
}

var controllerCaps = []csi.ControllerServiceCapability_RPC_Type{
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
	csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME, // attach/detach
	csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
	csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
}

func (c *ControllerService) validateCreateVolumeRequest(req *csi.CreateVolumeRequest) (bool, error) {
	if req == nil {
		return false, status.Error(codes.InvalidArgument, "missing request")
	}
	// Check arguments
	if len(req.GetName()) == 0 {
		return false, status.Error(codes.InvalidArgument, "name missing in request")
	}
	caps := req.GetVolumeCapabilities()

	if caps == nil {
		return false, status.Error(codes.InvalidArgument, "volume capabilities missing in request")
	}

	isBlock, isRWX := getAccessMode(caps)

	if isRWX && !isBlock {
		return false, status.Error(codes.InvalidArgument, "non-block volume with RWX access mode is not supported")
	}

	if c.storageClassEnforcement.AllowAll {
		return isRWX, nil
	}

	storageClassName := req.Parameters[client.InfraStorageClassNameParameter]
	if storageClassName == "" {
		if c.storageClassEnforcement.AllowDefault {
			return isRWX, nil
		} else {
			return false, unallowedStorageClass
		}
	}
	if !util.Contains(c.storageClassEnforcement.AllowList, storageClassName) {
		return false, unallowedStorageClass
	}

	return isRWX, nil
}

func getAccessMode(caps []*csi.VolumeCapability) (bool, bool) {
	isBlock := false
	isRWX := false

	for _, capability := range caps {
		if capability != nil {
			if capability.GetBlock() != nil {
				isBlock = true
			}

			if am := capability.GetAccessMode(); am != nil {
				if am.Mode == csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER {
					isRWX = true
				}
			}
		}
	}

	return isBlock, isRWX
}

// CreateVolume Create a new DataVolume.
// The new DataVolume.Name is csi.Volume.VolumeID.
// The new DataVolume.ID is used as the disk serial.
func (c *ControllerService) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if req != nil {
		klog.V(3).Infof("Create Volume Request: %s", req.String())
	}
	isRWX, err := c.validateCreateVolumeRequest(req)
	if err != nil {
		return nil, err
	}

	// Prepare parameters for the DataVolume
	storageClassName := req.Parameters[client.InfraStorageClassNameParameter]
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
	source, err := c.determineDvSource(ctx, req)
	if err != nil {
		return nil, err
	}

	dv := &cdiv1.DataVolume{
		TypeMeta: v1.TypeMeta{
			Kind:       "DataVolume",
			APIVersion: cdiv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      dvName,
			Namespace: c.infraClusterNamespace,
			Labels:    c.infraClusterLabels,
			Annotations: map[string]string{
				"cdi.kubevirt.io/storage.deleteAfterCompletion": "false",
			},
		},
		Spec: cdiv1.DataVolumeSpec{
			Storage: &cdiv1.StorageSpec{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: *resource.NewScaledQuantity(storageSize, 0)},
				},
			},
			Source: source,
		},
	}

	if isRWX {
		dv.Spec.Storage.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
	}

	// Only set the storageclass if it is defined. Otherwise we use the
	// default storage class which means leaving the storageclass empty
	// (nil) on the PVC
	if storageClassName != "" {
		dv.Spec.Storage.StorageClassName = &storageClassName
	}

	if existingDv, err := c.virtClient.GetDataVolume(ctx, c.infraClusterNamespace, dvName); errors.IsNotFound(err) {
		// Create DataVolume
		klog.Infof("creating new DataVolume %s/%s", c.infraClusterNamespace, req.Name)
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
			ContentSource: req.GetVolumeContentSource(),
		},
	}, nil
}

func (c *ControllerService) determineDvSource(ctx context.Context, req *csi.CreateVolumeRequest) (*cdiv1.DataVolumeSource, error) {
	res := &cdiv1.DataVolumeSource{}
	if req.GetVolumeContentSource() != nil {
		source := req.GetVolumeContentSource()
		switch source.Type.(type) {
		case *csi.VolumeContentSource_Snapshot:
			if snapshot, err := c.virtClient.GetVolumeSnapshot(ctx, c.infraClusterNamespace, source.GetSnapshot().GetSnapshotId()); errors.IsNotFound(err) {
				return nil, status.Errorf(codes.NotFound, "source snapshot content %s not found", source.GetSnapshot().GetSnapshotId())
			} else if err != nil {
				return nil, err
			} else if snapshot != nil {
				if snapshotSource := source.GetSnapshot(); snapshotSource != nil {
					res.Snapshot = &cdiv1.DataVolumeSourceSnapshot{
						Name:      snapshot.Name,
						Namespace: c.infraClusterNamespace,
					}
				}
			}
		default:
			return nil, status.Error(codes.InvalidArgument, "unknown content type")
		}
	} else {
		res.Blank = &cdiv1.DataVolumeBlankImage{}
	}
	return res, nil
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
		klog.V(3).Infof("Delete Volume Request: %s", req.String())
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
	if _, err := c.virtClient.GetDataVolume(ctx, c.infraClusterNamespace, dvName); errors.IsNotFound(err) {
		return nil, status.Errorf(codes.NotFound, "volume %s not found", req.GetVolumeId())
	} else if err != nil {
		return nil, err
	}

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

func (c *ControllerService) validateCreateSnapshotRequest(req *csi.CreateSnapshotRequest) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "missing request")
	}
	if len(req.GetName()) == 0 {
		return status.Error(codes.InvalidArgument, "name missing in request")
	}
	if len(req.GetSourceVolumeId()) == 0 {
		return status.Error(codes.InvalidArgument, "source volume id missing in request")
	}
	return nil
}

func (c *ControllerService) verifySourceVolumeExists(ctx context.Context, volumeID string) (bool, error) {
	dv, err := c.virtClient.GetDataVolume(ctx, c.infraClusterNamespace, volumeID)
	if errors.IsNotFound(err) {
		return false, nil
	}
	return dv != nil, err
}

func (c *ControllerService) CreateSnapshot(ctx context.Context, req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	if err := c.validateCreateSnapshotRequest(req); err != nil {
		return nil, err
	}

	var response *csi.CreateSnapshotResponse
	if existingSnapshot, err := c.virtClient.GetVolumeSnapshot(ctx, c.infraClusterNamespace, req.GetName()); errors.IsNotFound(err) {
		if exists, err := c.verifySourceVolumeExists(ctx, req.GetSourceVolumeId()); err != nil {
			return nil, err
		} else if !exists {
			return nil, status.Errorf(codes.NotFound, "source volume %s not found", req.GetSourceVolumeId())
		}
		// Prepare parameters for the DataVolume
		snapshotClassName := req.Parameters[client.InfraSnapshotClassNameParameter]
		volumeSnapshot, err := c.virtClient.CreateVolumeSnapshot(ctx, c.infraClusterNamespace, req.GetName(), req.GetSourceVolumeId(), snapshotClassName)
		if err != nil {
			return nil, err
		}
		// Need to wait for the snapshot to be ready in the infra cluster so we can properly report the size
		// to the volume snapshot in the tenant cluster. Otherwise the restore size will be 0.
		if err := c.virtClient.EnsureSnapshotReady(ctx, c.infraClusterNamespace, volumeSnapshot.Name, time.Minute*2); err != nil {
			return nil, err
		}
		volumeSnapshot, err = c.virtClient.GetVolumeSnapshot(ctx, c.infraClusterNamespace, volumeSnapshot.Name)
		if err != nil {
			return nil, err
		}
		response = createSnapshotResponse(volumeSnapshot)
	} else if err != nil {
		return nil, err
	} else {
		if !snapshotSourceMatchesVolume(existingSnapshot, req.GetSourceVolumeId()) {
			return nil, status.Errorf(codes.AlreadyExists, "snapshot with the same name: %s but with different SourceVolumeId already exist", req.GetName())
		}
		response = createSnapshotResponse(existingSnapshot)
	}
	return response, nil
}

func snapshotSourceMatchesVolume(snapshot *snapshotv1.VolumeSnapshot, volumeID string) bool {
	return snapshot.Spec.Source.PersistentVolumeClaimName != nil && *snapshot.Spec.Source.PersistentVolumeClaimName == volumeID
}

func createCsiSnapshot(snapshot *snapshotv1.VolumeSnapshot) *csi.Snapshot {
	res := &csi.Snapshot{
		SnapshotId:     snapshot.Name,
		SourceVolumeId: *snapshot.Spec.Source.PersistentVolumeClaimName,
		CreationTime:   timestamppb.New(snapshot.GetCreationTimestamp().Time),
		ReadyToUse:     false,
	}
	if snapshot.Status != nil {
		if snapshot.Status.ReadyToUse != nil {
			res.ReadyToUse = *snapshot.Status.ReadyToUse
		}
		if snapshot.Status.RestoreSize != nil {
			res.SizeBytes = snapshot.Status.RestoreSize.Value()
		}
	}
	return res
}

func createSnapshotResponse(snapshot *snapshotv1.VolumeSnapshot) *csi.CreateSnapshotResponse {
	return &csi.CreateSnapshotResponse{
		Snapshot: createCsiSnapshot(snapshot),
	}
}

func (c *ControllerService) DeleteSnapshot(ctx context.Context, req *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if len(req.GetSnapshotId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "snapshot id missing in request")
	}
	if err := c.virtClient.DeleteVolumeSnapshot(ctx, c.infraClusterNamespace, req.GetSnapshotId()); err != nil {
		return nil, err
	}
	return &csi.DeleteSnapshotResponse{}, nil
}

func (c *ControllerService) ListSnapshots(ctx context.Context, req *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}

	var items []snapshotv1.VolumeSnapshot

	if req.GetSnapshotId() != "" {
		if snapshot, err := c.virtClient.GetVolumeSnapshot(ctx, c.infraClusterNamespace, req.GetSnapshotId()); err != nil && !errors.IsNotFound(err) {
			return nil, err
		} else if snapshot != nil {
			items = append(items, *snapshot)
		}
	} else {
		snapshots, err := c.virtClient.ListVolumeSnapshots(ctx, c.infraClusterNamespace)
		if err != nil {
			return nil, err
		}
		if len(req.GetSourceVolumeId()) > 0 {
			// Search for the snapshot that matches the source volume id
			for _, snapshot := range snapshots.Items {
				if snapshotSourceMatchesVolume(&snapshot, req.GetSourceVolumeId()) {
					items = append(items, snapshot)
				}
			}
		} else {
			items = snapshots.Items
		}
	}

	if snapshotRes, err := createSnapshotResponseFromItems(req, items); err != nil {
		return nil, err
	} else {
		return snapshotRes, nil
	}
}

func createSnapshotResponseFromItems(req *csi.ListSnapshotsRequest, items []snapshotv1.VolumeSnapshot) (*csi.ListSnapshotsResponse, error) {
	snapshotRes := &csi.ListSnapshotsResponse{}
	if len(items) > 0 {
		snapshotRes.Entries = []*csi.ListSnapshotsResponse_Entry{}
		if req.StartingToken == "" || req.StartingToken == "0" {
			req.StartingToken = "1"
		}

		snapshotLength := int64(len(items))
		maxLength := int64(req.MaxEntries)
		if maxLength == 0 {
			maxLength = snapshotLength
		}
		start, err := strconv.ParseUint(req.StartingToken, 10, 32)
		if err != nil {
			return nil, err
		}
		start = start - 1
		end := int64(start) + maxLength

		if end > snapshotLength {
			end = snapshotLength
		}

		for _, val := range items[start:end] {
			snapshotRes.Entries = append(snapshotRes.Entries, &csi.ListSnapshotsResponse_Entry{
				Snapshot: createCsiSnapshot(&val),
			})
		}
		if end < snapshotLength-1 {
			snapshotRes.NextToken = strconv.FormatInt(end+1, 10)
		}
	}
	return snapshotRes, nil
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
