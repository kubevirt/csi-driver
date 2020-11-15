package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/mount"

	"github.com/container-storage-interface/spec/lib/go/csi"

	"golang.org/x/net/context"
	"k8s.io/klog"

	"github.com/kubevirt/csi-driver/pkg/kubevirt"
)

type NodeService struct {
	infraClusterClient kubernetes.Clientset
	kubevirtClient     kubevirt.Client
	nodeId             string
}

var NodeCaps = []csi.NodeServiceCapability_RPC_Type{
	csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
}

// NodeStageVolume prepares the volume for usage. If it's an FS type it creates a file system on the volume.
func (n *NodeService) NodeStageVolume(_ context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	// TODO remove the req struct from the log, it may contain sentitive info like secrets
	klog.Infof("Staging volume %s with %+v", req.VolumeId, req)

	// get the VMI volumes which are under VMI.spec.volumes
	// serialID = kubevirt's DataVolume.UID

	device, err := getDeviceBySerialID(req.VolumeContext[serialParameter])
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
	err = makeFS(device.Path, fsType)
	if err != nil {
		klog.Errorf("Could not create filesystem %s on %s", fsType, device)
		return nil, err
	}

	return &csi.NodeStageVolumeResponse{}, nil
}

func (n *NodeService) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	// nothing to do here, we don't erase the filesystem of a device.
	return &csi.NodeUnstageVolumeResponse{}, nil
}

//NodePublishVolume mounts the volume to the target path (req.GetTargetPath)
func (n *NodeService) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	// volumeID = serialID = kubevirt's DataVolume.metadata.uid
	// TODO link to kubevirt code
	device, err := getDeviceBySerialID(req.VolumeContext[serialParameter])
	if err != nil {
		klog.Errorf("Failed to fetch device by serialID %s ", req.VolumeId)
		return nil, err
	}

	targetPath := req.GetTargetPath()
	err = os.MkdirAll(targetPath, 0750)
	// MkdirAll returns nil if path already exists
	if err != nil {
		return nil, err
	}

	//TODO support mount options
	req.GetStagingTargetPath()
	fsType := req.VolumeCapability.GetMount().FsType
	klog.Infof("Mounting devicePath %s, on targetPath: %s with FS type: %s",
		device, targetPath, fsType)
	mounter := mount.New("")
	err = mounter.Mount(device.Path, targetPath, fsType, []string{})
	if err != nil {
		klog.Errorf("Failed mounting %v", err)
		return nil, err
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

//NodeUnpublishVolume unmount the volume from the worker node
func (n *NodeService) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	mounter := mount.New("")
	klog.Infof("Unmounting %s", req.GetTargetPath())
	err := mounter.Unmount(req.GetTargetPath())
	if err != nil {
		klog.Infof("Failed to unmount")
		return nil, err
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (n *NodeService) NodeGetVolumeStats(context.Context, *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	panic("implement me")
}

func (n *NodeService) NodeExpandVolume(context.Context, *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	panic("implement me")
}

func (n *NodeService) NodeGetInfo(context.Context, *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	// the nodeId is the VM's ID in kubevirt or VMI.spec.domain.firmware.uuid
	return &csi.NodeGetInfoResponse{NodeId: n.nodeId}, nil
}

func (n *NodeService) NodeGetCapabilities(context.Context, *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	caps := make([]*csi.NodeServiceCapability, 0, len(NodeCaps))
	for _, c := range NodeCaps {
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

type Devices struct {
	BlockDevices []Device `json:"blockdevices"`
}
type Device struct {
	SerialID string `json:"serial"`
	Path     string `json:"path"`
	Fstype   string `json:"fstype"`
}

func getDeviceBySerialID(serialID string) (Device, error) {
	klog.Infof("Get the device details by serialID %s", serialID)
	klog.V(5).Info("lsblk -nJo SERIAL,PATH,FSTYPE")

	// must be lsblk recent enough for json format
	cmd := exec.Command("lsblk", "-nJo", "SERIAL,PATH,FSTYPE")
	out, err := cmd.Output()
	exitError, incompleteCmd := err.(*exec.ExitError)
	if err != nil && incompleteCmd {
		return Device{}, errors.New(err.Error() + "lsblk failed with " + string(exitError.Stderr))
	}

	devices := Devices{}
	err = json.Unmarshal(out, &devices)
	if err != nil {
		klog.Errorf("Failed to parse json output from lsblk: %s", err)
		return Device{}, err
	}

	for _, d := range devices.BlockDevices {
		if d.SerialID == serialID {
			return d, nil
		}
	}
	return Device{}, errors.New("couldn't find device by serial id")
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
		klog.Errorf("stdout: %s", string(stdout.Bytes()))
		klog.Errorf("stderr: %s", string(stderr.Bytes()))
		return errors.New(err.Error() + " mkfs failed with " + exitError.Error())
	}

	return nil
}
