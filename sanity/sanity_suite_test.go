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
	identityClientset := createIdentityClient()
	virtClient, deviceLister := createVirtClient()

	service.NewMounter = func() mount.Interface {
		return &fakeMounter{
			values: make([]mountArgs, 0),
		}
	}
	service.NewDeviceLister = func() service.DeviceLister {
		return deviceLister
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
		nodeID,
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
