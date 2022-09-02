package sanity

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/kubernetes-csi/csi-test/v5/pkg/sanity"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"k8s.io/klog/v2"
	"k8s.io/utils/mount"
	"kubevirt.io/csi-driver/pkg/service"
)

var (
	tempDir    string
	err        error
	testConfig sanity.TestConfig
)

var _ = ginkgo.BeforeSuite(func() {
	tempDir, err = ioutil.TempDir(os.TempDir(), "csi-sanity")
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
	driver := service.NewKubevirtCSIDriver(virtClient,
		identityClientset,
		infraClusterNamespace,
		infraClusterLabelsMap,
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
