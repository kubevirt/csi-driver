package kubevirt

import (
	"context"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k8sv1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	cdicli "kubevirt.io/csi-driver/pkg/generated/containerized-data-importer/client-go/clientset/versioned/fake"
	snapfake "kubevirt.io/csi-driver/pkg/generated/external-snapshotter/client-go/clientset/versioned/fake"
	"kubevirt.io/csi-driver/pkg/util"
)

const (
	storageClassName             = "test-storage-class"
	defaultStorageClassName      = "default-storage-class"
	volumeSnapshotClassName      = "test-volume-snapshot-class"
	provisioner                  = "test-provisioner"
	nonMatchingProvisioner       = "non-matching-provisioner-snapshot-class"
	otherprovisioner             = "other-provisioner"
	otherVolumeSnapshotClassName = "other-volume-snapshot-class"
	testVolumeName               = "test-volume"
	testVolumeNameNotAllowed     = "test-volume-not-allowed"
	testClaimName                = "test-claim"
	testClaimName2               = "test-claim2"
	testClaimName3               = "test-claim3"
	testNamespace                = "test-namespace"
	unboundTestClaimName         = "unbound-test-claim"
)

var _ = Describe("Client", func() {
	var (
		c *client
	)

	Context("Snapshot class", func() {
		BeforeEach(func() {
			// Setup code before each test
			c = NewFakeClient()
		})

		DescribeTable("should return volume snapshot class or error", func(storageClassName, volumeSnapshotClassName, resultSnapshotClassName string, expectedError bool) {
			res, err := c.getSnapshotClassFromStorageClass(context.TODO(), storageClassName, volumeSnapshotClassName)
			if expectedError {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).ToNot(HaveOccurred())
				Expect(res.Name).To(Equal(resultSnapshotClassName))
			}
		},
			Entry("should return volume snapshot class", storageClassName, volumeSnapshotClassName, volumeSnapshotClassName, false),
			Entry("should return default snapshot class", storageClassName, "", otherVolumeSnapshotClassName, false),
			Entry("should return error with non existing storage class", "non-existing-storage-class", "", "", true),
			Entry("should return error when provider doesn't match", storageClassName, nonMatchingProvisioner, "", true),
		)

		It("Storage class from volume should return a storage class", func() {
			storageClass, err := c.getStorageClassFromVolume(context.TODO(), testVolumeName)
			Expect(err).ToNot(HaveOccurred())
			Expect(storageClass).To(Equal(storageClassName))
		})

		It("Storage class from volume should return error if getting volume returns an error", func() {
			storageClass, err := c.getStorageClassFromVolume(context.TODO(), "invalid")
			Expect(err).To(HaveOccurred())
			Expect(storageClass).To(Equal(""))
		})

		It("volume from claim should return a volume name", func() {
			volumeName, err := c.getVolumeNameFromClaimName(context.TODO(), testNamespace, testClaimName)
			Expect(err).ToNot(HaveOccurred())
			Expect(volumeName).To(Equal(testVolumeName))
		})

		It("volume from claim should return error if getting claim name returns an error", func() {
			volumeName, err := c.getVolumeNameFromClaimName(context.TODO(), testNamespace, "invalid")
			Expect(err).To(HaveOccurred())
			Expect(volumeName).To(Equal(""))
		})

		DescribeTable("should return snapshot class from claim or error", func(claimName, namespace, snapshotClassName, resultSnapshotClassName string, expectedError bool) {
			res, err := c.getSnapshotClassNameFromVolumeClaimName(context.TODO(), namespace, claimName, snapshotClassName)
			if expectedError {
				Expect(err).To(HaveOccurred())
				Expect(res).To(Equal(""))
			} else {
				Expect(err).ToNot(HaveOccurred())
				Expect(res).To(Equal(resultSnapshotClassName))
			}
		},
			Entry("should return snapshot class", testClaimName, testNamespace, volumeSnapshotClassName, volumeSnapshotClassName, false),
			Entry("should return error when claim is invalid", "invalid", testNamespace, volumeSnapshotClassName, "", true),
			Entry("should return error when claim is unbound", unboundTestClaimName, testNamespace, volumeSnapshotClassName, "", true),
			Entry("should return error when volume cannot be found", testClaimName2, testNamespace, volumeSnapshotClassName, "", true),
		)

		It("should return error if the storage class is not allowed", func() {
			res, err := c.getSnapshotClassNameFromVolumeClaimName(context.TODO(), testNamespace, testClaimName3, volumeSnapshotClassName)
			Expect(err).To(HaveOccurred())
			Expect(res).To(Equal(""))
			Expect(err.Error()).To(ContainSubstring("not allowed for snapshot creation"))
		})

		It("should return error if the storage class is not allowed", func() {
			c.storageClassEnforcement.AllowAll = true
			c.storageClassEnforcement.AllowList = nil
			_, err := c.getSnapshotClassNameFromVolumeClaimName(context.TODO(), testNamespace, testClaimName, volumeSnapshotClassName)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("Snapshot operators", func() {
		createValidDataVolume := func(name string) *cdiv1.DataVolume {
			return &cdiv1.DataVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: testNamespace,
				},
				Spec: cdiv1.DataVolumeSpec{},
			}
		}

		BeforeEach(func() {
			// Setup code before each test
			c = NewFakeCdiClient(NewFakeClient(), createValidDataVolume(testClaimName))
		})

		It("should return error if the volume snapshot class is not found", func() {
			s, err := c.CreateVolumeSnapshot(context.TODO(), testNamespace, "snap", testClaimName, "non-existing-snapshot-class")
			Expect(err).To(HaveOccurred())
			Expect(s).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("provided volume snapshot class cannot be matched with storage class"))
		})

		It("should return error if the DV is not found", func() {
			s, err := c.CreateVolumeSnapshot(context.TODO(), testNamespace, "snap", "invalid", volumeSnapshotClassName)
			Expect(err).To(HaveOccurred())
			Expect(s).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("should delete volumesnapshot if it exists and it valid", func() {
			s, err := c.CreateVolumeSnapshot(context.TODO(), testNamespace, "snap", testClaimName, volumeSnapshotClassName)
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Name).To(Equal("snap"))
			err = c.DeleteVolumeSnapshot(context.TODO(), s.GetNamespace(), s.GetName())
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil if the volumesnapshot is not found", func() {
			err := c.DeleteVolumeSnapshot(context.TODO(), testNamespace, "notfound")
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return error if get volume returns an error", func() {
			s, err := c.CreateVolumeSnapshot(context.TODO(), testNamespace, "snap", testClaimName, volumeSnapshotClassName)
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Name).To(Equal("snap"))
			c.infraLabelMap = map[string]string{"test": "test"}
			err = c.DeleteVolumeSnapshot(context.TODO(), s.GetNamespace(), s.GetName())
			Expect(err).To(Equal(ErrInvalidSnapshot))
		})

		It("should properly list snapshots", func() {
			s, err := c.CreateVolumeSnapshot(context.TODO(), testNamespace, "snap", testClaimName, volumeSnapshotClassName)
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Name).To(Equal("snap"))
			l, err := c.ListVolumeSnapshots(context.TODO(), testNamespace)
			Expect(err).ToNot(HaveOccurred())
			Expect(l.Items).To(HaveLen(1))
			By("Changing the valid labels, we should now not get results")
			c.infraLabelMap = map[string]string{"test2": "test"}
			l, err = c.ListVolumeSnapshots(context.TODO(), testNamespace)
			Expect(err).ToNot(HaveOccurred())
			Expect(l.Items).To(BeEmpty())
		})
	})

	Context("Storage class enforcement", func() {
		BeforeEach(func() {
			// Setup code before each test
			c = NewFakeClient()
		})

		DescribeTable("should properly calculate if storage class is allowed", func(storageClassName string, enforcement util.StorageClassEnforcement, expected bool) {
			c.storageClassEnforcement = enforcement
			res, err := c.isStorageClassAllowed(context.TODO(), storageClassName)
			Expect(err).ToNot(HaveOccurred())
			Expect(res).To(Equal(expected))
		},
			Entry("should return true if storage class is in allowedList", storageClassName,
				util.StorageClassEnforcement{AllowList: []string{storageClassName}}, true),
			Entry("should return false if storage class is not in allowedList", storageClassName,
				util.StorageClassEnforcement{AllowList: []string{}}, false),
			Entry("should return true if default but not in allowedList", defaultStorageClassName,
				util.StorageClassEnforcement{AllowList: []string{}, AllowDefault: true}, true),
			Entry("should return false if not default and not in allowedList", storageClassName,
				util.StorageClassEnforcement{AllowList: []string{}, AllowDefault: true}, false),
		)
	})
})

func NewFakeClient() *client {
	storageClass := createStorageClass(storageClassName, provisioner, false)
	defaultStorageClass := createStorageClass(defaultStorageClassName, provisioner, true)
	testVolume := createPersistentVolume(testVolumeName, storageClassName)
	testVolumeNotAllowed := createPersistentVolume(testVolumeNameNotAllowed, "not-allowed-storage-class")
	testClaim := createPersistentVolumeClaim(testClaimName, testVolumeName, storageClassName)
	testClaim2 := createPersistentVolumeClaim(testClaimName2, "testVolumeName2", storageClassName)
	testClaim3 := createPersistentVolumeClaim(testClaimName3, testVolumeNameNotAllowed, "not-allowed-storage-class")
	unboundClaim := &k8sv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      unboundTestClaimName,
			Namespace: testNamespace,
		},
		Spec: k8sv1.PersistentVolumeClaimSpec{
			StorageClassName: ptr.To[string](storageClassName),
		},
	}
	fakeK8sClient := k8sfake.NewSimpleClientset(storageClass, defaultStorageClass, testVolume,
		testVolumeNotAllowed, testClaim, testClaim2, testClaim3, unboundClaim)

	fakeSnapClient := snapfake.NewSimpleClientset(
		createVolumeSnapshotClass(volumeSnapshotClassName, provisioner, false),
		createVolumeSnapshotClass(nonMatchingProvisioner, otherprovisioner, false),
		createVolumeSnapshotClass(otherVolumeSnapshotClassName, provisioner, true),
	)
	result := &client{
		kubernetesClient: fakeK8sClient,
		snapClient:       fakeSnapClient,
		storageClassEnforcement: util.StorageClassEnforcement{
			AllowList:    []string{storageClassName},
			AllowAll:     false,
			AllowDefault: true,
		},
	}
	return result
}

func NewFakeCdiClient(c *client, objects ...runtime.Object) *client {
	fakeCdiClient := cdicli.NewSimpleClientset(objects...)
	c.cdiClient = fakeCdiClient
	return c
}

func createVolumeSnapshotClass(name, provisioner string, isDefault bool) *snapshotv1.VolumeSnapshotClass {
	res := &snapshotv1.VolumeSnapshotClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Driver: provisioner,
	}
	if isDefault {
		res.Annotations = map[string]string{
			"snapshot.storage.kubernetes.io/is-default-class": "true",
		}
	}
	return res
}

func createPersistentVolume(name, storageClassName string) *k8sv1.PersistentVolume {
	return &k8sv1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: k8sv1.PersistentVolumeSpec{
			StorageClassName: storageClassName,
		},
	}
}

func createPersistentVolumeClaim(name, volumeName, storageClassName string) *k8sv1.PersistentVolumeClaim {
	return &k8sv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: k8sv1.PersistentVolumeClaimSpec{
			StorageClassName: ptr.To[string](storageClassName),
			VolumeName:       volumeName,
		},
	}
}

func createStorageClass(name, provisioner string, isDefault bool) *storagev1.StorageClass {
	res := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Provisioner: provisioner,
	}
	if isDefault {
		res.Annotations = map[string]string{
			"storageclass.kubernetes.io/is-default-class": "true",
		}
	}
	return res
}
