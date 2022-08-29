package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"k8s.io/utils/mount"

	"github.com/container-storage-interface/spec/lib/go/csi"

	"golang.org/x/net/context"
	klog "k8s.io/klog/v2"
)

var nodeCaps = []csi.NodeServiceCapability_RPC_Type{
	csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
}

// NodeService implements the CSI Driver node service
type NodeService struct {
	nodeID       string
	deviceLister deviceLister
	fsMaker      fsMaker
	fsMounter    mount.Interface
	dirMaker     dirMaker
}

type deviceLister interface{ List() ([]byte, error) }
type fsMaker interface {
	Make(device string, fsType string) error
}
type dirMaker interface {
	Make(path string, perm os.FileMode) error
}

func NewNodeService(nodeId string) *NodeService {
	return &NodeService{
		nodeID: nodeId,
		deviceLister: deviceListerFunc(func() ([]byte, error) {
			klog.V(5).Info("lsblk -nJo SERIAL,FSTYPE,NAME")
			// must be lsblk recent enough for json format
			return exec.Command("lsblk", "-nJo", "SERIAL,FSTYPE,NAME").Output()
		}),
		fsMaker: fsMakerFunc(func(device, fsType string) error {
			return makeFS(device, fsType)
		}),
		fsMounter: mount.New(""),
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

type fsMakerFunc func(device, fsType string) error

func (f fsMakerFunc) Make(device, fsType string) error {
	return f(device, fsType)
}

type dirMakerFunc func(path string, perm os.FileMode) error

func (d dirMakerFunc) Make(path string, perm os.FileMode) error {
	return d(path, perm)
}

// NodeStageVolume prepares the volume for usage. If it's an FS type it creates a file system on the volume.
func (n *NodeService) NodeStageVolume(_ context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	klog.Infof("Staging volume %s", req.VolumeId)

	if req.VolumeCapability.GetBlock() != nil {
		err := fmt.Errorf("block mode is not supported")
		klog.Error(err)
		return nil, err
	}

	// get the VMI volumes which are under VMI.spec.volumes
	// serialID = kubevirt's DataVolume.UID

	device, err := getDeviceBySerialID(req.VolumeContext[serialParameter], n.deviceLister)
	if err != nil {
		klog.Errorf("Failed to fetch device by serialID %s", req.VolumeId)
		return nil, err
	}

	// is there a filesystem on this device?
	if device.Fstype != "" {
		klog.Infof("Detected fs %s", device.Fstype)
		return &csi.NodeStageVolumeResponse{}, nil
	}

	fsType := req.VolumeCapability.GetMount().FsType
	// no filesystem - create it
	klog.Infof("Creating FS %s on device %s", fsType, device)
	err = n.fsMaker.Make(device.Path, fsType)
	if err != nil {
		klog.Errorf("Could not create filesystem %s on %s", fsType, device)
		return nil, err
	}

	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume unstages a volume from the node
func (n *NodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	// nothing to do here, we don't erase the filesystem of a device.
	return &csi.NodeUnstageVolumeResponse{}, nil
}

//NodePublishVolume mounts the volume to the target path (req.GetTargetPath)
func (n *NodeService) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	// volumeID = serialID = kubevirt's DataVolume.metadata.uid
	// TODO link to kubevirt code
	device, err := getDeviceBySerialID(req.VolumeContext[serialParameter], n.deviceLister)
	if err != nil {
		klog.Errorf("Failed to fetch device by serialID %s ", req.VolumeId)
		return nil, err
	}

	targetPath := req.GetTargetPath()
	err = n.dirMaker.Make(targetPath, 0750)
	// MkdirAll returns nil if path already exists
	if err != nil {
		return nil, err
	}

	//TODO support mount options
	req.GetStagingTargetPath()
	fsType := req.VolumeCapability.GetMount().FsType
	klog.Infof("Mounting devicePath %s, on targetPath: %s with FS type: %s",
		device, targetPath, fsType)
	err = n.fsMounter.Mount(device.Path, targetPath, fsType, []string{})
	if err != nil {
		klog.Errorf("Failed mounting %v", err)
		return nil, err
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

//NodeUnpublishVolume unmount the volume from the worker node
func (n *NodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	klog.Infof("Unmounting %s", req.GetTargetPath())
	err := n.fsMounter.Unmount(req.GetTargetPath())
	if err != nil {
		klog.Infof("Failed to unmount")
		return nil, err
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetVolumeStats unimplemented
func (n *NodeService) NodeGetVolumeStats(context.Context, *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	panic("implement me")
}

// NodeExpandVolume unimplemented
func (n *NodeService) NodeExpandVolume(context.Context, *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	panic("implement me")
}

// NodeGetInfo returns the node ID
func (n *NodeService) NodeGetInfo(context.Context, *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	// the nodeID is the VM's ID in kubevirt or VMI.spec.domain.firmware.uuid
	return &csi.NodeGetInfoResponse{NodeId: n.nodeID}, nil
}

//NodeGetCapabilities returns the supported capabilities of the node service
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

func getDeviceBySerialID(serialID string, deviceLister deviceLister) (device, error) {
	klog.Infof("Get the device details by serialID %s", serialID)

	out, err := deviceLister.List()
	exitError, incompleteCmd := err.(*exec.ExitError)
	if err != nil && incompleteCmd {
		return device{}, errors.New(err.Error() + "lsblk failed with " + string(exitError.Stderr))
	}

	devices := devices{}
	err = json.Unmarshal(out, &devices)
	if err != nil {
		klog.Errorf("Failed to parse json output from lsblk: %s", err)
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
	if strings.HasPrefix(fsType, "ext") {
		cmd = exec.Command("mkfs", "-F", "-t", fsType, device)
	} else if strings.HasPrefix(fsType, "xfs") {
		cmd = exec.Command("mkfs", "-t", fsType, "-f", device)
	} else {
		return errors.New(fsType + " is not supported, only xfs and ext are supported")
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
