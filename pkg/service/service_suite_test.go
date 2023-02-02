package service

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/utils/mount"
)

const serialID = "4b13cebc-7406-4c19-8832-7fcb1d4ac8c5"

func TestService(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Service Suite")
}

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
		underTest.mounter = successfulMounter{}
	})

	Describe("Staging a volume", func() {
		Context("With non-matching serial ID", func() {
			It("should fail", func() {
				_, err := underTest.NodeStageVolume(context.TODO(), &csi.NodeStageVolumeRequest{
					VolumeId:      "pvc-123",
					VolumeContext: map[string]string{serialParameter: "serial000"},
				})
				Expect(err).To(HaveOccurred())
			})
		})

		Context("With Block mode", func() {
			It("should succeed", func() {
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
		})

		Context("With failure to make new filesystem", func() {
			It("should fail", func() {
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
		})

		Context("With successful make new filesystem", func() {
			It("should succeed", func() {
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
	})

	Describe("Publishing a volume", func() {
		Context("With non-matching serial ID", func() {
			It("should fail", func() {
				res, err := underTest.NodePublishVolume(context.TODO(), &csi.NodePublishVolumeRequest{
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

				res, err := underTest.NodePublishVolume(context.TODO(), newPublishRequest())
				Expect(err).To(HaveOccurred())
				Expect(res).To(BeNil())
			})
		})

		Context("With matching serial ID and failing mount", func() {
			It("should fail", func() {
				underTest.mounter = failingMounter{}
				res, err := underTest.NodePublishVolume(context.TODO(), newPublishRequest())
				Expect(err).To(HaveOccurred())
				Expect(res).To(BeNil())
			})
		})

		Context("With matching serial ID and successful mount", func() {
			It("should succeed", func() {
				res, err := underTest.NodePublishVolume(context.TODO(), newPublishRequest())
				Expect(err).ToNot(HaveOccurred())
				Expect(res).ToNot(BeNil())
			})
		})
	})

	Describe("Un-Publishing a volume", func() {
		Context("With failing umount", func() {
			It("should fail", func() {
				underTest.mounter = failingMounter{}
				res, err := underTest.NodeUnpublishVolume(context.TODO(), &csi.NodeUnpublishVolumeRequest{
					VolumeId: "pvc-123",
				})
				Expect(err).To(HaveOccurred())
				Expect(res).To(BeNil())
			})
		})
	})

})

var _ = Describe("IdentityService", func() {
	var (
		mockCtrl  *gomock.Controller
		underTest IdentityService
		mockProbe *fakeProber
	)

	BeforeEach(func() {
		mockProbe = &fakeProber{}
		mockCtrl = gomock.NewController(GinkgoT())
		underTest = IdentityService{connectivityProbe: mockProbe}
	})

	Describe("Get Plugin Info", func() {
		res, err := underTest.GetPluginInfo(context.Background(), &csi.GetPluginInfoRequest{})
		It("should not fail", func() {
			Expect(err).NotTo(HaveOccurred())
		})
		It("should return vendor name", func() {
			Expect(res.Name).To(Equal(VendorName))
		})
		It("should return vendor version", func() {
			Expect(res.VendorVersion).To(Equal(VendorVersion))
		})
	})

	Describe("Get Plugin Capabilities", func() {
		res, err := underTest.GetPluginCapabilities(context.Background(), &csi.GetPluginCapabilitiesRequest{})
		It("should not fail", func() {
			Expect(err).NotTo(HaveOccurred())
		})
		It("should return list of capabilities", func() {
			Expect(res.Capabilities).Should(Not(BeEmpty()))
		})
	})

	Describe("Call Probe", func() {
		var (
			err error
			res *csi.ProbeResponse
		)
		Context("When the probe fails", func() {
			BeforeEach(func() {
				mockProbe.err = fmt.Errorf("error")
				res, err = underTest.Probe(context.Background(), &csi.ProbeRequest{})
			})
			It("should fail with error", func() {
				Expect(err).To(HaveOccurred())
			})

			It("should return a nil response", func() {
				Expect(res).To(BeNil())
			})

		})
		Context("When the probe succeeds", func() {
			BeforeEach(func() {
				mockProbe.err = nil
				res, err = underTest.Probe(context.Background(), &csi.ProbeRequest{})
			})
			It("should not return error", func() {
				Expect(err).ToNot(HaveOccurred())
			})

			It("should return a Ready response", func() {
				Expect(res.GetReady().Value).Should(BeTrue())
			})

		})
	})

	AfterEach(func() {
		mockCtrl.Finish()
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
	return true, nil
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
