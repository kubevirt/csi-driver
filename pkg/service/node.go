package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	mount "k8s.io/mount-utils"
	utilexec "k8s.io/utils/exec"

	"github.com/container-storage-interface/spec/lib/go/csi"

	"golang.org/x/net/context"
	klog "k8s.io/klog/v2"

	"kubevirt.io/csi-driver/pkg/mounter"
)

var (
	nodeCaps = []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
		csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
	}
	ErrMountDeviceNotFound = errors.New("could not find device path for mount")
)

// NodeService implements the CSI Driver node service
type NodeService struct {
	csi.UnimplementedNodeServer
	nodeID           string
	deviceLister     DeviceLister
	fsMaker          FsMaker
	mounter          mounter.Mounter
	resizer          ResizerInterface
	devicePathGetter DevicePathGetter
	dirMaker         dirMaker
}

type DeviceLister interface {
	List() ([]byte, error)
}
type DevicePathGetter interface {
	Get(mountPath string) (string, error)
}
type FsMaker interface {
	Make(device string, fsType string) error
}
type dirMaker interface {
	Make(path string, perm os.FileMode) error
}

type ResizerInterface interface {
	NeedResize(devicePath string, deviceMountPath string) (bool, error)
	Resize(devicePath, deviceMountPath string) (bool, error)
}

var NewResizer = func() ResizerInterface {
	return mount.NewResizeFs(utilexec.New())
}

var NewDeviceLister = func() DeviceLister {
	return deviceListerFunc(func() ([]byte, error) {
		klog.V(5).Info("lsblk -nJo SERIAL,FSTYPE,NAME")
		// must be lsblk recent enough for json format
		return exec.Command("lsblk", "-nJo", "SERIAL,FSTYPE,NAME").Output()
	})
}

var NewDevicePathGetter = func() DevicePathGetter {
	return devicePathGetterFunc(func(mountPath string) (string, error) {
		args := []string{"-o", "source", "--nofsroot", "--noheadings", "--target", mountPath}
		klog.V(5).Info(args)
		out, err := exec.Command("findmnt", args...).Output()
		if err != nil {
			return "", err
		}
		devicePath := strings.TrimSpace(string(out))
		klog.V(5).Info(devicePath)
		if filepath.IsAbs(devicePath) {
			// sanity check output
			return devicePath, nil
		}

		return "", ErrMountDeviceNotFound
	})
}

var NewNodeMounter = func() mounter.Mounter {
	return mounter.NewNodeMounter()
}

var NewFsMaker = func() FsMaker {
	return fsMakerFunc(func(device, fsType string) error {
		return makeFS(device, fsType)
	})
}

func NewNodeService(nodeId string) *NodeService {
	return &NodeService{
		nodeID:           nodeId,
		deviceLister:     NewDeviceLister(),
		devicePathGetter: NewDevicePathGetter(),
		fsMaker:          NewFsMaker(),
		mounter:          NewNodeMounter(),
		resizer:          NewResizer(),
		dirMaker: dirMakerFunc(func(path string, perm os.FileMode) error {
			// MkdirAll returns nil if path already exists
			return os.MkdirAll(path, perm)
		}),
	}
}

type deviceListerFunc func() ([]byte, error)

func (d deviceListerFunc) List() ([]byte, error) {
	return d()
}

type devicePathGetterFunc func(mountPath string) (string, error)

func (d devicePathGetterFunc) Get(mountPath string) (string, error) {
	return d(mountPath)
}

type fsMakerFunc func(device, fsType string) error

func (f fsMakerFunc) Make(device, fsType string) error {
	return f(device, fsType)
}

type dirMakerFunc func(path string, perm os.FileMode) error

func (d dirMakerFunc) Make(path string, perm os.FileMode) error {
	return d(path, perm)
}

func (n *NodeService) validateNodeStageVolumeRequest(req *csi.NodeStageVolumeRequest) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "missing request")
	}
	// Check ID and targetPath
	if len(req.GetVolumeId()) == 0 {
		return status.Error(codes.InvalidArgument, "volume ID not provided")
	}
	if req.GetStagingTargetPath() == "" {
		return status.Error(codes.InvalidArgument, "staging target path not provided")
	}
	if req.GetVolumeCapability() == nil {
		return status.Error(codes.InvalidArgument, "volume capability not provided")
	}
	return nil
}

// NodeStageVolume prepares the volume for usage. If it's an FS type it creates a file system on the volume.
func (n *NodeService) NodeStageVolume(_ context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	if err := n.validateNodeStageVolumeRequest(req); err != nil {
		return nil, err
	}
	klog.V(3).Infof("Staging volume %s", req.VolumeId)

	if req.VolumeCapability.GetMount() == nil {
		return &csi.NodeStageVolumeResponse{}, nil
	}

	// Filesystem volume mode, create FS if needed
	// get the VMI volumes which are under VMI.spec.volumes
	// serialID = kubevirt's DataVolume.UID
	device, err := getDeviceBySerialID(req.VolumeContext[serialParameter], n.deviceLister)
	if err != nil {
		klog.Errorf("Failed to fetch device by serialID %s", req.VolumeId)
		return nil, err
	}

	// is there a filesystem on this device?
	if device.Fstype != "" {
		klog.V(3).Infof("Detected fs %s", device.Fstype)
		return &csi.NodeStageVolumeResponse{}, nil
	}

	fsType := req.VolumeCapability.GetMount().FsType
	// no filesystem - create it
	klog.V(3).Infof("Creating FS %s on device %s", fsType, device)
	err = n.fsMaker.Make(device.Path, fsType)
	if err != nil {
		klog.Errorf("Could not create filesystem %s on %s", fsType, device)
		return nil, err
	}

	return &csi.NodeStageVolumeResponse{}, nil
}

func (n *NodeService) validateNodeUnstageVolumeRequest(req *csi.NodeUnstageVolumeRequest) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "missing request")
	}
	// Check ID and targetPath
	if len(req.GetVolumeId()) == 0 {
		return status.Error(codes.InvalidArgument, "volume ID not provided")
	}
	if req.GetStagingTargetPath() == "" {
		return status.Error(codes.InvalidArgument, "staging target path not provided")
	}
	return nil
}

// NodeUnstageVolume unstages a volume from the node
func (n *NodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	if err := n.validateNodeUnstageVolumeRequest(req); err != nil {
		klog.Errorf("Validate Node unstage failed %v", err)
		return nil, err
	}
	klog.V(3).Info("Validate Node unstage completed")
	// nothing to do here, we don't erase the filesystem of a device.
	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (n *NodeService) validateRequestCapabilties(req *csi.NodePublishVolumeRequest) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "missing request")
	}
	// Check ID and targetPath
	if len(req.GetVolumeId()) == 0 {
		return status.Error(codes.InvalidArgument, "volume ID not provided")
	}
	if req.GetTargetPath() == "" {
		return status.Error(codes.InvalidArgument, "target path not provided")
	}
	if req.GetVolumeCapability() == nil {
		return status.Error(codes.InvalidArgument, "volume capability not provided")
	}
	return nil
}

// validateNodePublishRequest validates that the request contains all the required elements.
func (n *NodeService) validateNodePublishRequest(req *csi.NodePublishVolumeRequest) error {
	if err := n.validateRequestCapabilties(req); err != nil {
		return err
	}

	if req.GetVolumeCapability().GetBlock() != nil && req.GetVolumeCapability().GetMount() != nil {
		return status.Error(codes.InvalidArgument, "cannot publish volume as both block and filesystem")
	}
	if req.GetVolumeCapability().GetMount() == nil && req.GetVolumeCapability().GetBlock() == nil {
		return status.Error(codes.InvalidArgument, "volume mode is not specified")
	}

	return nil
}

// NodePublishVolume mounts the volume to the target path (req.GetTargetPath)
func (n *NodeService) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	if req != nil {
		klog.V(3).Infof("Node Publish Request: %s", req.String())
	}
	if err := n.validateNodePublishRequest(req); err != nil {
		return nil, err
	}
	fsType := ""
	if req.VolumeCapability.GetMount() != nil {
		fsType = req.VolumeCapability.GetMount().FsType
	}

	mountOptions := []string{}
	block := req.GetVolumeCapability().GetBlock() != nil
	if block {
		mountOptions = append(mountOptions, "bind")
	} else if fsType == "xfs" {
		// Add nouuid to fix duplicate XFS uuid when restoring from snapshot.
		// Alternatively we could run xfs_admin -U generate <device> to generate a new UUID
		mountOptions = append(mountOptions, "nouuid")
	}

	// volumeID = serialID = kubevirt's DataVolume.metadata.uid
	// TODO link to kubevirt code
	device, err := getDeviceBySerialID(req.VolumeContext[serialParameter], n.deviceLister)
	if err != nil {
		klog.Errorf("failed to fetch device by serialID %s ", req.VolumeId)
		return nil, err
	}

	targetPath := req.GetTargetPath()
	notMnt, err := n.mounter.IsLikelyNotMountPoint(targetPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if notMnt {
		if err := n.mountToTargetPath(req, block, targetPath, device, fsType, mountOptions); err != nil {
			return nil, err
		}
	}

	if !block {
		if err := n.resizeFs(device.Path, targetPath); err != nil {
			return nil, err
		}
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (n *NodeService) resizeFs(devicePath, targetPath string) error {
	ok, err := n.resizer.NeedResize(devicePath, targetPath)
	if err != nil {
		return status.Errorf(codes.Internal,
			"need resize check failed on devicePath %s and targetPath %s, error: %v",
			devicePath,
			targetPath,
			err)
	}
	if !ok {
		// no resize is required
		return nil
	}
	ok, err = n.resizer.Resize(devicePath, targetPath)
	if !ok || err != nil {
		return status.Errorf(codes.Internal,
			"resize failed on path %s, error: %v", targetPath, err)
	}

	return nil
}

func (n *NodeService) mountToTargetPath(req *csi.NodePublishVolumeRequest, isBlock bool, targetPath string, device device, fsType string, mountOptions []string) error {
	if isBlock {
		if err := n.ensureMountFileExists(targetPath); err != nil {
			return err
		}
	} else {
		// MkdirAll returns nil if path already exists
		if err := n.dirMaker.Make(targetPath, 0750); err != nil {
			return err
		}
		klog.V(3).Infof("GetMount() %v", req.VolumeCapability.GetMount())
		klog.V(3).Infof("Mounting devicePath %s, on targetPath: %s with FS type: %s",
			device, targetPath, fsType)
	}

	if err := n.mounter.Mount(device.Path, targetPath, fsType, mountOptions); err != nil {
		klog.Errorf("failed mounting %v", err)
		return err
	}

	return nil
}

// Make sure target file exists for bind mount
func (n *NodeService) ensureMountFileExists(mountFile string) error {
	_, err := os.Stat(mountFile)

	if err == nil {
		return nil
	} else if errors.Is(err, os.ErrNotExist) {
		var f *os.File
		f, err = os.OpenFile(mountFile, os.O_CREATE, os.FileMode(0640))
		if err != nil {
			return err
		}
		defer f.Close()
	}
	return err
}

func (n *NodeService) validateNodeUnpublishRequest(req *csi.NodeUnpublishVolumeRequest) error {
	// Check arguments
	if len(req.GetVolumeId()) == 0 {
		return status.Error(codes.InvalidArgument, "volume ID not provided")
	}
	if len(req.GetTargetPath()) == 0 {
		return status.Error(codes.InvalidArgument, "target path not provided")
	}
	return nil
}

// NodeUnpublishVolume unmount the volume from the worker node
func (n *NodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	if req != nil {
		klog.V(3).Infof("Node Unpublish Request: %s", req.String())
	}
	if err := n.validateNodeUnpublishRequest(req); err != nil {
		klog.Errorf("Validate node unpublish failed %v", err)
		return nil, err
	}

	targetPath := req.GetTargetPath()
	klog.V(5).Infof("Unmounting %s", targetPath)
	err := mount.CleanupMountPoint(targetPath, n.mounter, false /* extensive mount point checks not needed, no bind mounts within same mount point */)
	if err != nil {
		klog.Errorf("failed to cleanup mount point %v", err)
		return nil, err
	}

	if err = os.RemoveAll(targetPath); err != nil {
		klog.Errorf("failed to remove %s, %v", targetPath, err)
		return nil, fmt.Errorf("remove target path: %w", err)
	}
	klog.V(3).Info("Validate Node unpublish completed")

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (n *NodeService) NodeGetVolumeStats(_ context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	klog.V(4).InfoS("NodeGetVolumeStats: called", "args", req)
	if len(req.GetVolumeId()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume ID was empty")
	}
	if len(req.GetVolumePath()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume path was empty")
	}

	exists, err := mount.PathExists(req.GetVolumePath())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "unknown error when stat on %s: %v", req.GetVolumePath(), err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "path %s does not exist", req.GetVolumePath())
	}

	isBlock, err := n.mounter.IsBlockDevice(req.GetVolumePath())

	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to determine whether %s is block device: %v", req.GetVolumePath(), err)
	}
	if isBlock {
		bcap, blockErr := n.mounter.GetBlockSizeBytes(req.GetVolumePath())
		if blockErr != nil {
			return nil, status.Errorf(codes.Internal, "failed to get block capacity on path %s: %v", req.GetVolumePath(), blockErr)
		}
		return &csi.NodeGetVolumeStatsResponse{
			Usage: []*csi.VolumeUsage{
				{
					Unit:  csi.VolumeUsage_BYTES,
					Total: bcap,
				},
			},
		}, nil
	}

	stats, err := n.mounter.GetVolumeStats(req.GetVolumePath())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get fs info on path %s: %v", req.GetVolumePath(), err)
	}

	usage := []*csi.VolumeUsage{
		{
			Unit:      csi.VolumeUsage_BYTES,
			Available: stats.AvailableBytes,
			Total:     stats.TotalBytes,
			Used:      stats.UsedBytes,
		},
	}
	if stats.TotalInodes != 0 {
		usage = append(usage, &csi.VolumeUsage{
			Unit:      csi.VolumeUsage_INODES,
			Available: stats.AvailableInodes,
			Total:     stats.TotalInodes,
			Used:      stats.UsedInodes,
		})
	}

	return &csi.NodeGetVolumeStatsResponse{
		Usage: usage,
	}, nil
}

// NodeExpandVolume only gets invoked for filesystem volumes and takes care of expanding the filesystem
func (n *NodeService) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	volumePath := req.GetVolumePath()

	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no volume_id is provided")
	}
	if len(volumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "no volume_path is provided")
	}

	block := req.GetVolumeCapability().GetBlock() != nil
	if block {
		klog.V(2).Infof("NodeExpandVolume is not needed for block volume %s", volumeID)
		return &csi.NodeExpandVolumeResponse{}, nil
	}

	devicePath, err := n.devicePathGetter.Get(volumePath)
	if err != nil {
		if !errors.Is(err, ErrMountDeviceNotFound) {
			return nil, err
		}
		return nil, status.Errorf(codes.NotFound, "device path for %s not found", volumePath)
	}

	if err := n.resizeFs(devicePath, volumePath); err != nil {
		return nil, err
	}

	return &csi.NodeExpandVolumeResponse{}, nil
}

// NodeGetInfo returns the node ID
func (n *NodeService) NodeGetInfo(context.Context, *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	// the nodeID is the VM's ID in kubevirt or VMI.spec.domain.firmware.uuid
	return &csi.NodeGetInfoResponse{NodeId: n.nodeID}, nil
}

// NodeGetCapabilities returns the supported capabilities of the node service
func (n *NodeService) NodeGetCapabilities(context.Context, *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	caps := make([]*csi.NodeServiceCapability, 0, len(nodeCaps))
	for _, c := range nodeCaps {
		caps = append(
			caps,
			&csi.NodeServiceCapability{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: c,
					},
				},
			},
		)
	}
	return &csi.NodeGetCapabilitiesResponse{Capabilities: caps}, nil
}

type devices struct {
	BlockDevices []device `json:"blockdevices"`
}

type device struct {
	SerialID string `json:"serial"`
	Path     string `json:"path,omitempty"`
	Name     string `json:"name"`
	Fstype   string `json:"fstype"`
}

func getDeviceBySerialID(serialID string, deviceLister DeviceLister) (device, error) {
	klog.Infof("Get the device details by serialID %s", serialID)

	out, err := deviceLister.List()
	exitError, incompleteCmd := err.(*exec.ExitError)
	if err != nil && incompleteCmd {
		return device{}, errors.New(err.Error() + "lsblk failed with " + string(exitError.Stderr))
	}

	devices := devices{}
	err = json.Unmarshal(out, &devices)
	if err != nil {
		klog.Errorf("failed to parse json output from lsblk: %s", err)
		return device{}, err
	}

	for _, d := range devices.BlockDevices {
		if d.SerialID == serialID {
			d.Path = "/dev/" + d.Name
			return d, nil
		}
	}
	return device{}, errors.New("couldn't find device by serial id")
}

func makeFS(device string, fsType string) error {
	// caution, use force flag when creating the filesystem if it doesn't exit.
	klog.Infof("Mounting device %s, with FS %s", device, fsType)

	var cmd *exec.Cmd
	var stdout, stderr bytes.Buffer
	if strings.HasPrefix(fsType, "ext4") {
		// Don't reserve root space on ext4, since these volumes are mounted it makes no sense
		// to reserve the space.
		cmd = exec.Command("mkfs", "-m", "0", "-F", "-t", fsType, device)
	} else if strings.HasPrefix(fsType, "xfs") {
		cmd = exec.Command("mkfs", "-t", fsType, "-f", device)
	} else {
		return errors.New(fsType + " is not supported, only xfs and ext4 are supported")
	}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitError, incompleteCmd := err.(*exec.ExitError)
	if err != nil && incompleteCmd {
		klog.Errorf("stdout: %s", stdout.String())
		klog.Errorf("stderr: %s", stdout.String())
		return errors.New(err.Error() + " mkfs failed with " + exitError.Error())
	}

	return nil
}
