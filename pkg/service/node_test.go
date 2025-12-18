package service

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/net/context"
	"k8s.io/klog/v2"
	mount "k8s.io/mount-utils"

	"kubevirt.io/csi-driver/pkg/mounter"
)

const serialID = "4b13cebc-7406-4c19-8832-7fcb1d4ac8c5"

type fakeProber struct {
	err error
}

func (m *fakeProber) Probe() error {
	return m.err
}

var _ = Describe("NodeService", func() {
	var (
		underTest NodeService
		logOutput *bytes.Buffer
		argsFile  string
	)

	BeforeEach(func() {
		underTest = NodeService{
			nodeID: "vm-worker-0-0",
		}
		underTest.deviceLister = deviceListerFunc(func() ([]byte, error) {
			json := fmt.Sprintf("{\"blockdevices\": [{\"serial\":\"%s\", \"name\":\"%s\", \"fstype\":null}]}", serialID, "sdc")
			return []byte(json), nil
		})
		underTest.dirMaker = dirMakerFunc(func(string, os.FileMode) error {
			return nil
		})
		underTest.devicePathGetter = devicePathGetterFunc(func(mountPath string) (string, error) {
			return "/dev/sdc", nil
		})
		underTest.mounter = &successfulMounter{}
		underTest.resizer = noopResizer{}

		logOutput, argsFile = setupFakeUdevadm()
	})

	Context("Staging a volume", func() {
		It("should fail with non-matching serial ID", func() {
			_, err := underTest.NodeStageVolume(context.TODO(), &csi.NodeStageVolumeRequest{
				VolumeId:      "pvc-123",
				VolumeContext: map[string]string{serialParameter: "serial000"},
			})
			Expect(err).To(HaveOccurred())
		})

		It("should succeed with Block mode", func() {
			res, err := underTest.NodeStageVolume(context.TODO(), &csi.NodeStageVolumeRequest{
				VolumeId: "pvc-123",
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
				},
				VolumeContext:     map[string]string{serialParameter: serialID},
				StagingTargetPath: "/invalid/staging",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(res).ToNot(BeNil())
		})

		It("should fail with failure to make new filesystem", func() {
			underTest.fsMaker = fsMakerFunc(func(device, path string) error {
				return fmt.Errorf("unknown fs")
			})
			_, err := underTest.NodeStageVolume(context.TODO(), &csi.NodeStageVolumeRequest{
				VolumeId: "pvc-123",
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{
							FsType: "uknownFs",
						},
					},
				},
				VolumeContext:     map[string]string{serialParameter: serialID},
				StagingTargetPath: "/invalid/staging",
			})
			Expect(err).To(HaveOccurred())
		})

		It("should succeed successful make new filesystem", func() {
			underTest.fsMaker = fsMakerFunc(func(device, path string) error {
				return nil
			})
			res, err := underTest.NodeStageVolume(context.TODO(), &csi.NodeStageVolumeRequest{
				VolumeId: "pvc-123",
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{
							FsType: "ext4",
						},
					},
				},
				VolumeContext:     map[string]string{serialParameter: serialID},
				StagingTargetPath: "/invalid/staging",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(res).ToNot(BeNil())
		})

		It("should call udevadm with correct arguments", func() {
			underTest.deviceLister = deviceListerFunc(func() ([]byte, error) {
				json := fmt.Sprintf("{\"blockdevices\": [{\"name\":\"%s\", \"fstype\":null}]}", "sdc")
				return []byte(json), nil
			})

			if err := os.Setenv("MOCK_UDEVADM_EXIT_CODE", "0"); err != nil {
				Fail(err.Error())
			}

			res, err := underTest.NodeStageVolume(context.TODO(), &csi.NodeStageVolumeRequest{
				VolumeId: "pvc-123",
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{
							FsType: "ext4",
						},
					},
				},
				VolumeContext:     map[string]string{serialParameter: serialID},
				StagingTargetPath: "/invalid/staging",
			})
			Expect(err).To(HaveOccurred())
			Expect(res).To(BeNil())

			klog.Flush()

			logs := logOutput.String()
			argsData, err := os.ReadFile(argsFile)
			if err != nil {
				Fail(err.Error())
			}

			Expect(string(argsData)).To(Equal("trigger --action=change --name-match=/dev/sdc"))
			Expect(logs).ToNot(ContainSubstring("failed"))
			Expect(logs).To(ContainSubstring("stdout: test stdout"))
		})

		It("should add udevadm verbose flag if klog verbosity is high", func() {
			underTest.deviceLister = deviceListerFunc(func() ([]byte, error) {
				json := fmt.Sprintf("{\"blockdevices\": [{\"name\":\"%s\", \"fstype\":null}]}", "sdc")
				return []byte(json), nil
			})

			if err := os.Setenv("MOCK_UDEVADM_EXIT_CODE", "0"); err != nil {
				Fail(err.Error())
			}

			// Set verbosity level for this test
			var fs flag.FlagSet
			klog.InitFlags(&fs)
			if err := fs.Set("v", "5"); err != nil {
				Fail(err.Error())
			}

			res, err := underTest.NodeStageVolume(context.TODO(), &csi.NodeStageVolumeRequest{
				VolumeId: "pvc-123",
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{
							FsType: "ext4",
						},
					},
				},
				VolumeContext:     map[string]string{serialParameter: serialID},
				StagingTargetPath: "/invalid/staging",
			})
			Expect(err).To(HaveOccurred())
			Expect(res).To(BeNil())

			klog.Flush()

			logs := logOutput.String()
			argsData, err := os.ReadFile(argsFile)
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(string(argsData))).To(Equal("trigger --action=change --name-match=/dev/sdc --verbose"))
			Expect(logs).To(ContainSubstring("stdout: test stdout"))
		})

		It("should log udevadm error and stderr on command failure", func() {
			underTest.deviceLister = deviceListerFunc(func() ([]byte, error) {
				json := fmt.Sprintf("{\"blockdevices\": [{\"name\":\"%s\", \"fstype\":null}]}", "sdc")
				return []byte(json), nil
			})

			if err := os.Setenv("MOCK_UDEVADM_EXIT_CODE", "1"); err != nil {
				Fail(err.Error())
			}

			res, err := underTest.NodeStageVolume(context.TODO(), &csi.NodeStageVolumeRequest{
				VolumeId: "pvc-123",
				VolumeCapability: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{
							FsType: "ext4",
						},
					},
				},
				VolumeContext:     map[string]string{serialParameter: serialID},
				StagingTargetPath: "/invalid/staging",
			})
			Expect(err).To(HaveOccurred())
			Expect(res).To(BeNil())

			klog.Flush()

			logs := logOutput.String()
			Expect(logs).To(ContainSubstring("stdout: test stdout"))
			Expect(logs).To(ContainSubstring("stderr: test stderr"))
			Expect(logs).To(ContainSubstring("udev rescan for device /dev/sdc failed: exit status 1"))
		})
	})

	Context("Publishing a volume", func() {
		It("should fail with non-matching serial ID", func() {
			res, err := underTest.NodePublishVolume(context.TODO(), &csi.NodePublishVolumeRequest{
				VolumeId:      "pvc-123",
				VolumeContext: map[string]string{serialParameter: "serial000"},
			})
			Expect(err).To(HaveOccurred())
			Expect(res).To(BeNil())
		})

		It("should fail with failing mkdir", func() {
			underTest.dirMaker = dirMakerFunc(func(s string, mode os.FileMode) error {
				return fmt.Errorf("fail to create path s")
			})

			res, err := underTest.NodePublishVolume(context.TODO(), newPublishRequest())
			Expect(err).To(HaveOccurred())
			Expect(res).To(BeNil())
		})

		It("should fail with matching serial ID and failing mount", func() {
			underTest.mounter = &failingMounter{}
			res, err := underTest.NodePublishVolume(context.TODO(), newPublishRequest())
			Expect(err).To(HaveOccurred())
			Expect(res).To(BeNil())
		})

		It("should succeed, with matching serial ID and successful mount", func() {
			res, err := underTest.NodePublishVolume(context.TODO(), newPublishRequest())
			Expect(err).ToNot(HaveOccurred())
			Expect(res).ToNot(BeNil())
		})

		It("should perform a resize when it's required", func() {
			resizer := &successfulResizer{}
			underTest.resizer = resizer
			res, err := underTest.NodePublishVolume(context.TODO(), newPublishRequest())
			Expect(err).ToNot(HaveOccurred())
			Expect(res).ToNot(BeNil())
			Expect(resizer.resizeOccured).To(BeTrue())
		})

		It("should continue to resize call despite mount existing", func() {
			// Simulates a retry of NodePublishVolume following an error during resize
			underTest.resizer = &successfulResizer{}
			// Simulate a mount already existing since it was performed
			// in the first iteration
			underTest.mounter = &noopMounter{}
			res, err := underTest.NodePublishVolume(context.TODO(), newPublishRequest())
			Expect(err).ToNot(HaveOccurred())
			Expect(res).ToNot(BeNil())
			Expect(underTest.mounter.(*noopMounter).mountOccured).To(BeFalse())
			Expect(underTest.resizer.(*successfulResizer).resizeOccured).To(BeTrue())
		})
	})

	Context("Un-Publishing a volume", func() {
		It("should fail with failing umount", func() {
			underTest.mounter = &failingMounter{}
			res, err := underTest.NodeUnpublishVolume(context.TODO(), &csi.NodeUnpublishVolumeRequest{
				VolumeId: "pvc-123",
			})
			Expect(err).To(HaveOccurred())
			Expect(res).To(BeNil())
		})
	})

	Context("Node expanding a volume", func() {
		It("should resize fs volume", func() {
			resizer := &successfulResizer{}
			underTest.resizer = resizer
			res, err := underTest.NodeExpandVolume(context.TODO(),
				&csi.NodeExpandVolumeRequest{
					VolumeId: "pvc-123",
					VolumeCapability: &csi.VolumeCapability{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{
								FsType: "ext4",
							},
						},
					},
					VolumePath: "/target/path",
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(res).ToNot(BeNil())
			Expect(resizer.resizeOccured).To(BeTrue())
		})

		It("should not resize block volume", func() {
			resizer := &successfulResizer{}
			underTest.resizer = resizer
			res, err := underTest.NodeExpandVolume(context.TODO(),
				&csi.NodeExpandVolumeRequest{
					VolumeId: "pvc-123",
					VolumeCapability: &csi.VolumeCapability{
						AccessType: &csi.VolumeCapability_Block{
							Block: &csi.VolumeCapability_BlockVolume{},
						},
					},
					VolumePath: "/target/path",
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(res).ToNot(BeNil())
			Expect(resizer.resizeOccured).To(BeFalse())
		})
	})

	Context("Get node volume stats", func() {
		It("should get node volume stats metrics for block devices", func() {
			tmpDir := GinkgoT().TempDir()

			sMounter := &successfulMounter{
				isBlock: true,
			}
			underTest.mounter = sMounter
			res, err := underTest.NodeGetVolumeStats(context.TODO(),
				&csi.NodeGetVolumeStatsRequest{
					VolumeId:   "pvc-123",
					VolumePath: tmpDir,
				},
			)
			Expect(res.GetUsage()).To(HaveLen(1))
			Expect(res.GetUsage()[0].GetTotal()).To(Equal(int64(2048)))
			Expect(err).ToNot(HaveOccurred())
			Expect(res).ToNot(BeNil())
		})

		It("should get node volume stats metrics for non block devices", func() {
			tmpDir := GinkgoT().TempDir()

			sMounter := &successfulMounter{
				isBlock: false,
			}
			underTest.mounter = sMounter
			res, err := underTest.NodeGetVolumeStats(context.TODO(),
				&csi.NodeGetVolumeStatsRequest{
					VolumeId:   "pvc-123",
					VolumePath: tmpDir,
				},
			)
			Expect(res.GetUsage()).To(HaveLen(2))
			Expect(res.GetUsage()[0].GetTotal()).To(Equal(int64(2048)))
			Expect(res.GetUsage()[0].GetAvailable()).To(Equal(int64(1024)))
			Expect(res.GetUsage()[0].GetUsed()).To(Equal(int64(1024)))

			Expect(res.GetUsage()[1].GetTotal()).To(Equal(int64(6)))
			Expect(res.GetUsage()[1].GetAvailable()).To(Equal(int64(3)))
			Expect(res.GetUsage()[1].GetUsed()).To(Equal(int64(3)))
			Expect(err).ToNot(HaveOccurred())
			Expect(res).ToNot(BeNil())
		})
	})
})

func setupFakeUdevadm() (*bytes.Buffer, string) {
	// Create temp directory for udevadm output
	var err error
	tmpDir, err := os.MkdirTemp("", "rescan-test")
	if err != nil {
		Fail(err.Error())
	}

	// Create file to capture udevadm input and output
	argsFile := filepath.Join(tmpDir, "args.txt")

	// Create udevadm script to preempt udevadm cli commands
	script := fmt.Sprintf(`#!/bin/bash
			echo -n "$@" > %s
			echo "test stdout"
			echo "test stderr" >&2
			exit $MOCK_UDEVADM_EXIT_CODE`,
		argsFile)
	if err := os.WriteFile(filepath.Join(tmpDir, "udevadm"), []byte(script), 0700); err != nil {
		Fail(err.Error())
	}

	// Add fake udevadm script to PATH to execute instead of OS package
	originalPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", tmpDir+":"+originalPath); err != nil {
		Fail(err.Error())
	}

	// Return PATH after test
	DeferCleanup(func() {
		if err := os.Setenv("PATH", originalPath); err != nil {
			Fail(err.Error())
		}

		if err := os.RemoveAll(tmpDir); err != nil {
			Fail(err.Error())
		}
	})

	// Correctly configure klog to write to a buffer for testing
	logOutput := &bytes.Buffer{}
	fs := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(fs)
	if err := fs.Set("logtostderr", "false"); err != nil {
		Fail(err.Error())
	}
	klog.SetOutput(logOutput)

	// Restore default klog settings after test
	DeferCleanup(func() {
		klog.SetOutput(os.Stderr)
		fs := flag.NewFlagSet("klog", flag.ExitOnError)
		klog.InitFlags(fs) // Re-init to get defaults
	})

	return logOutput, argsFile
}

func newPublishRequest() *csi.NodePublishVolumeRequest {
	return &csi.NodePublishVolumeRequest{
		VolumeId:      "pvc-123",
		VolumeContext: map[string]string{serialParameter: serialID},
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{
					FsType: "ext4",
				},
			},
		},
		TargetPath: "/target/path",
	}
}

type noopResizer struct{}

func (r noopResizer) Resize(devicePath, deviceMountPath string) (bool, error) {
	return false, nil
}

func (r noopResizer) NeedResize(devicePath string, deviceMountPath string) (bool, error) {
	return false, nil
}

type successfulResizer struct {
	resizeOccured bool
}

func (r *successfulResizer) Resize(devicePath, deviceMountPath string) (bool, error) {
	r.resizeOccured = true
	return true, nil
}

func (r *successfulResizer) NeedResize(devicePath string, deviceMountPath string) (bool, error) {
	return true, nil
}

type noopMounter struct {
	successfulMounter
}

func (m *noopMounter) IsLikelyNotMountPoint(file string) (bool, error) {
	return false, nil
}

type successfulMounter struct {
	mountOccured bool
	isBlock      bool
}

type failingMounter struct {
	successfulMounter
}

func (s *successfulMounter) PathExists(path string) (bool, error) {
	return true, nil
}

func (s *successfulMounter) IsBlockDevice(fullPath string) (bool, error) {
	return s.isBlock, nil
}

func (s *successfulMounter) GetBlockSizeBytes(devicePath string) (int64, error) {
	return 2048, nil
}

func (s *successfulMounter) GetVolumeStats(volumePath string) (mounter.VolumeStats, error) {
	return mounter.VolumeStats{
		AvailableBytes:  1024,
		TotalBytes:      2048,
		UsedBytes:       1024,
		AvailableInodes: 3,
		TotalInodes:     6,
		UsedInodes:      3,
	}, nil
}

func (m *successfulMounter) Mount(source string, target string, fstype string, options []string) error {
	m.mountOccured = true
	return nil
}

func (m *successfulMounter) MountSensitive(source string, target string, fstype string, options []string, sensitiveOptions []string) error {
	return nil
}

func (m *successfulMounter) Unmount(target string) error {
	return nil
}

func (m *successfulMounter) List() ([]mount.MountPoint, error) {
	panic("implement me")
}

func (m *successfulMounter) IsLikelyNotMountPoint(file string) (bool, error) {
	return true, nil
}

func (m *successfulMounter) GetMountRefs(pathname string) ([]string, error) {
	panic("implement me")
}

func (m *successfulMounter) CanSafelySkipMountPointCheck() bool {
	panic("implement me")
}

func (m *successfulMounter) IsMountPoint(file string) (bool, error) {
	panic("implement me")
}

func (m *successfulMounter) MountSensitiveWithoutSystemd(source string, target string, fstype string, options []string, sensitiveOptions []string) error {
	panic("implement me")
}

func (m *successfulMounter) MountSensitiveWithoutSystemdWithMountFlags(source string, target string, fstype string, options []string, sensitiveOptions []string, mountFlags []string) error {
	panic("implement me")
}

func (f *failingMounter) Mount(source string, target string, fstype string, options []string) error {
	return fmt.Errorf("failing mounter always fails")
}

func (f *failingMounter) Unmount(target string) error {
	return fmt.Errorf("failing unmounter always fails")
}
