package kubevirt

import (
	"context"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	k8sv1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	snapfake "kubevirt.io/csi-driver/pkg/generated/external-snapshotter/client-go/clientset/versioned/fake"
)

const (
	storageClassName             = "test-storage-class"
	volumeSnapshotClassName      = "test-volume-snapshot-class"
	provisioner                  = "test-provisioner"
	nonMatchingProvisioner       = "non-matching-provisioner-snapshot-class"
	otherprovisioner             = "other-provisioner"
	otherVolumeSnapshotClassName = "other-volume-snapshot-class"
	testVolumeName               = "test-volume"
	testClaimName                = "test-claim"
	testClaimName2               = "test-claim2"
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
	})
})

func NewFakeClient() *client {
	storageClass := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: storageClassName,
		},
		Provisioner: provisioner,
	}
	testVolume := &k8sv1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: testVolumeName,
		},
		Spec: k8sv1.PersistentVolumeSpec{
			StorageClassName: storageClassName,
		},
	}
	testClaim := &k8sv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClaimName,
			Namespace: testNamespace,
		},
		Spec: k8sv1.PersistentVolumeClaimSpec{
			StorageClassName: ptr.To[string](storageClassName),
			VolumeName:       testVolumeName,
		},
	}
	testClaim2 := &k8sv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClaimName2,
			Namespace: testNamespace,
		},
		Spec: k8sv1.PersistentVolumeClaimSpec{
			StorageClassName: ptr.To[string](storageClassName),
			VolumeName:       "testVolumeName2",
		},
	}
	unboundClaim := &k8sv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      unboundTestClaimName,
			Namespace: testNamespace,
		},
		Spec: k8sv1.PersistentVolumeClaimSpec{
			StorageClassName: ptr.To[string](storageClassName),
		},
	}
	fakeK8sClient := k8sfake.NewSimpleClientset(storageClass, testVolume, testClaim, testClaim2, unboundClaim)

	fakeSnapClient := snapfake.NewSimpleClientset(
		createVolumeSnapshotClass(volumeSnapshotClassName, provisioner, false),
		createVolumeSnapshotClass(nonMatchingProvisioner, otherprovisioner, false),
		createVolumeSnapshotClass(otherVolumeSnapshotClassName, provisioner, true),
	)
	result := &client{
		kubernetesClient: fakeK8sClient,
		snapClient:       fakeSnapClient,
	}
	return result
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
