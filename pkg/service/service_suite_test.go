package service

import (
	"fmt"
	"os"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/utils/mount"
)

const serialID = "4b13cebc-7406-4c19-8832-7fcb1d4ac8c5"

var ctrl *gomock.Controller

func TestService(t *testing.T) {
	ctrl := gomock.NewController(t)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Service Suite")
	defer ctrl.Finish()
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
		underTest.fsMounter = successfulMounter{}
	})

	Describe("Staging a volume", func() {
		Context("With non-matching serial ID", func() {
			It("should fail", func() {
				_, err := underTest.NodeStageVolume(nil, &csi.NodeStageVolumeRequest{
					VolumeId:      "pvc-123",
					VolumeContext: map[string]string{serialParameter: "serial000"},
				})
				Expect(err).To(HaveOccurred())
			})
		})

		Context("With Block mode", func() {
			It("should fail", func() {
				_, err := underTest.NodeStageVolume(nil, &csi.NodeStageVolumeRequest{
					VolumeId: "pvc-123",
					VolumeCapability: &csi.VolumeCapability{
						AccessType: &csi.VolumeCapability_Block{
							Block: &csi.VolumeCapability_BlockVolume{},
						},
					},
					VolumeContext: map[string]string{serialParameter: serialID},
				})
				Expect(err).To(HaveOccurred())
			})
		})

		Context("With failure to make new filesystem", func() {
			It("should fail", func() {
				underTest.fsMaker = fsMakerFunc(func(device, path string) error {
					return fmt.Errorf("unknown fs")
				})
				_, err := underTest.NodeStageVolume(nil, &csi.NodeStageVolumeRequest{
					VolumeId: "pvc-123",
					VolumeCapability: &csi.VolumeCapability{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{
								FsType: "uknownFs",
							},
						},
					},
					VolumeContext: map[string]string{serialParameter: serialID},
				})
				Expect(err).To(HaveOccurred())
			})
		})

		Context("With successful make new filesystem", func() {
			It("should succeed", func() {
				underTest.fsMaker = fsMakerFunc(func(device, path string) error {
					return nil
				})
				res, err := underTest.NodeStageVolume(nil, &csi.NodeStageVolumeRequest{
					VolumeId: "pvc-123",
					VolumeCapability: &csi.VolumeCapability{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{
								FsType: "ext4",
							},
						},
					},
					VolumeContext: map[string]string{serialParameter: serialID},
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(res).ToNot(BeNil())
			})
		})
	})

	Describe("Publishing a volume", func() {
		Context("With non-matching serial ID", func() {
			It("should fail", func() {
				res, err := underTest.NodePublishVolume(nil, &csi.NodePublishVolumeRequest{
					VolumeId:      "pvc-123",
					VolumeContext: map[string]string{serialParameter: "serial000"},
				})
				Expect(err).To(HaveOccurred())
				Expect(res).To(BeNil())
			})
		})

		Context("With failing mkdir", func() {
			It("should fail", func() {
				underTest.dirMaker = dirMakerFunc(func(s string, mode os.FileMode) error {
					return fmt.Errorf("fail to create path s")
				})

				res, err := underTest.NodePublishVolume(nil, newPublishRequest())
				Expect(err).To(HaveOccurred())
				Expect(res).To(BeNil())
			})
		})

		Context("With matching serial ID and failing mount", func() {
			It("should fail", func() {
				underTest.fsMounter = failingMounter{}
				res, err := underTest.NodePublishVolume(nil, newPublishRequest())
				Expect(err).To(HaveOccurred())
				Expect(res).To(BeNil())
			})
		})

		Context("With matching serial ID and successful mount", func() {
			It("should succeed", func() {
				res, err := underTest.NodePublishVolume(nil, newPublishRequest())
				Expect(err).ToNot(HaveOccurred())
				Expect(res).ToNot(BeNil())
			})
		})
	})

	Describe("Un-Publishing a volume", func() {
		Context("With failing umount", func() {
			It("should fail", func() {
				underTest.fsMounter = failingMounter{}
				res, err := underTest.NodeUnpublishVolume(nil, &csi.NodeUnpublishVolumeRequest{
					VolumeId: "pvc-123",
				})
				Expect(err).To(HaveOccurred())
				Expect(res).To(BeNil())
			})
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
	}
}

type successfulMounter struct{}

type failingMounter struct {
	successfulMounter
}

func (m successfulMounter) Mount(source string, target string, fstype string, options []string) error {
	return nil
}

func (m successfulMounter) MountSensitive(source string, target string, fstype string, options []string, sensitiveOptions []string) error {
	return nil
}

func (m successfulMounter) Unmount(target string) error {
	return nil
}

func (m successfulMounter) List() ([]mount.MountPoint, error) {
	panic("implement me")
}

func (m successfulMounter) IsLikelyNotMountPoint(file string) (bool, error) {
	panic("implement me")
}

func (m successfulMounter) GetMountRefs(pathname string) ([]string, error) {
	panic("implement me")
}

func (f failingMounter) Mount(source string, target string, fstype string, options []string) error {
	return fmt.Errorf("failing mounter always fails")
}

func (f failingMounter) Unmount(target string) error {
	return fmt.Errorf("failing unmounter always fails")
}
