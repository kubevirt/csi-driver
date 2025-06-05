package service

import (
	"fmt"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/net/context"
	mount "k8s.io/mount-utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
	)
	BeforeEach(func() {
		underTest = NodeService{
			nodeID: "vm-worker-0-0",
		}
		underTest.deviceLister = deviceListerFunc(func() ([]byte, error) {
			json := fmt.Sprintf("{\"blockdevices\": [{\"serial\":\"%s\", \"path\":\"%s\", \"fstype\":null}]}", serialID, "/dev/sdc")
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
			Expect(len(res.GetUsage())).To(Equal(1))
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
			Expect(len(res.GetUsage())).To(Equal(2))
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
