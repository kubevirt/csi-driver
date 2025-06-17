package mounter

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	mountutils "k8s.io/mount-utils"
	utilexec "k8s.io/utils/exec"
)

// Mounter defines the interface used by NodeMounter. It combines methods from various upstream libraries
// (e.g., FormatAndMount from SafeFormatAndMount, PathExists from an older version of mount.Interface).
// This explicit definition allows for easier mocking and provides insulation from frequently changing upstream
// interfaces and structs.
type Mounter interface {
	mountutils.Interface

	PathExists(path string) (bool, error)
	IsBlockDevice(fullPath string) (bool, error)
	GetBlockSizeBytes(devicePath string) (int64, error)
	GetVolumeStats(volumePath string) (VolumeStats, error)
}

// NodeMounter implements Mounter.
type NodeMounter struct {
	*mountutils.SafeFormatAndMount
}

// NewNodeMounter returns a new intsance of NodeMounter.
func NewNodeMounter() Mounter {
	safeMounter := &mountutils.SafeFormatAndMount{
		Interface: mountutils.New(""),
		Exec:      utilexec.New(),
	}

	return &NodeMounter{safeMounter}
}

func (nm *NodeMounter) PathExists(path string) (bool, error) {
	return mountutils.PathExists(path)
}

func (nm *NodeMounter) IsBlockDevice(fullPath string) (bool, error) {
	var st unix.Stat_t
	err := unix.Stat(fullPath, &st)
	if err != nil {
		return false, err
	}

	return (st.Mode & unix.S_IFMT) == unix.S_IFBLK, nil
}

func (nm *NodeMounter) GetBlockSizeBytes(devicePath string) (int64, error) {
	output, err := nm.Exec.Command("blockdev", "--getsize64", devicePath).Output()
	if err != nil {
		return -1, fmt.Errorf("error when getting size of block volume at path %s: output: %s, err: %w", devicePath, string(output), err)
	}
	strOut := strings.TrimSpace(string(output))
	gotSizeBytes, err := strconv.ParseInt(strOut, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("failed to parse size %s as int", strOut)
	}
	return gotSizeBytes, nil
}

func (nm *NodeMounter) GetVolumeStats(volumePath string) (VolumeStats, error) {
	stats := VolumeStats{}

	statfs := &unix.Statfs_t{}
	err := unix.Statfs(volumePath, statfs)
	if err != nil {
		return stats, err
	}

	// Get byte stats (safely)
	bsize := statfs.Bsize
	if bsize < 0 {
		return stats, fmt.Errorf("negative block size reported: %d", bsize)
	}
	ubsize := uint64(bsize)

	if statfs.Bavail > math.MaxUint64/ubsize {
		return stats, errors.New("available bytes calculation would overflow")
	}
	availBytes := statfs.Bavail * ubsize
	if availBytes > math.MaxInt64 {
		return stats, errors.New("available bytes value exceeds int64 maximum")
	}
	stats.AvailableBytes = int64(availBytes)

	if statfs.Blocks > math.MaxUint64/ubsize {
		return stats, errors.New("total bytes calculation would overflow")
	}
	totBytes := statfs.Blocks * ubsize
	if totBytes > math.MaxInt64 {
		return stats, errors.New("total bytes value exceeds int64 maximum")
	}
	stats.TotalBytes = int64(totBytes)

	var usedBlocks uint64
	if statfs.Blocks > statfs.Bfree {
		usedBlocks = statfs.Blocks - statfs.Bfree
	} else {
		usedBlocks = 0
	}

	if usedBlocks > math.MaxUint64/ubsize {
		return stats, errors.New("used bytes calculation would overflow")
	}
	usedBytes := usedBlocks * ubsize
	if usedBytes > math.MaxInt64 {
		return stats, errors.New("used bytes value exceeds int64 maximum")
	}
	stats.UsedBytes = int64(usedBytes)

	// Get inode stats (safely)
	if statfs.Ffree > math.MaxInt64 {
		return stats, errors.New("available inodes value exceeds int64 maximum")
	}
	stats.AvailableInodes = int64(statfs.Ffree)

	if statfs.Files > math.MaxInt64 {
		return stats, errors.New("total inodes value exceeds int64 maximum")
	}
	stats.TotalInodes = int64(statfs.Files)

	if stats.TotalInodes < stats.AvailableInodes {
		return stats, errors.New("inconsistent inode counts: total < available")
	}
	stats.UsedInodes = stats.TotalInodes - stats.AvailableInodes

	return stats, nil
}

// VolumeStats holds volume stats returned by GetVolumeStats.
type VolumeStats struct {
	AvailableBytes int64
	TotalBytes     int64
	UsedBytes      int64

	AvailableInodes int64
	TotalInodes     int64
	UsedInodes      int64
}
