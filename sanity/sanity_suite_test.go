package sanity

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kubernetes-csi/csi-test/v5/pkg/sanity"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"k8s.io/klog/v2"
	mount "k8s.io/mount-utils"
	"kubevirt.io/csi-driver/pkg/service"
	"kubevirt.io/csi-driver/pkg/util"
)

var (
	tempDir    string
	err        error
	testConfig sanity.TestConfig
)

var _ = ginkgo.BeforeSuite(func() {
	tempDir, err = os.MkdirTemp(os.TempDir(), "csi-sanity")
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	// Test labels
	infraClusterLabelsMap := map[string]string{}
	hotpluggedMap := map[string]device{}
	identityClientset := createIdentityClient()
	virtClient := createVirtClient(hotpluggedMap)
	deviceLister := &fakeDeviceLister{
		hotpluggedMap: hotpluggedMap,
	}
	// needs to be pointer otherwise each append() assignment
	// changes the slice header in just one of them
	mountValues := &[]mountArgs{}

	service.NewMounter = func() mount.Interface {
		return &fakeMounter{
			values: mountValues,
		}
	}
	service.NewResizer = func() service.ResizerInterface {
		return &fakeResizer{}
	}
	service.NewDeviceLister = func() service.DeviceLister {
		return deviceLister
	}
	service.NewDevicePathGetter = func() service.DevicePathGetter {
		return &fakeDevicePathGetter{
			mountArgs: mountValues,
		}
	}
	service.NewFsMaker = func() service.FsMaker {
		return &fakeFsMaker{}
	}

	storagClassEnforcement := util.StorageClassEnforcement{
		AllowAll:     true,
		AllowDefault: true,
	}

	driver := service.NewKubevirtCSIDriver(virtClient,
		identityClientset,
		infraClusterNamespace,
		infraClusterLabelsMap,
		storagClassEnforcement,
		getKey(infraClusterNamespace, nodeID),
		true,
		true)
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	go func() {
		endpoint := "unix://" + filepath.Join(tempDir, sanityEndpoint)
		driver.Run(endpoint)
	}()
	testConfig = sanity.NewTestConfig()
	// Set configuration options as needed
	testConfig.Address = filepath.Join(tempDir, sanityEndpoint)
	testConfig.StagingPath = filepath.Join(tempDir, "csi-staging")
	testConfig.TargetPath = filepath.Join(tempDir, "csi-mount")
	klog.Infof("endpoint %s", testConfig.Address)
})

var _ = ginkgo.AfterSuite(func() {
	gomega.Expect(os.RemoveAll(tempDir)).To(gomega.Succeed())
})

func TestSuite(t *testing.T) {
	defer ginkgo.GinkgoRecover()
	gomega.RegisterFailHandler(ginkgo.Fail)
	klog.SetOutput(ginkgo.GinkgoWriter)
	ginkgo.RunSpecs(t, "Tests Suite")
}
