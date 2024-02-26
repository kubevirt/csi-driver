package e2e_test

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8sv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	snapcli "kubevirt.io/csi-driver/pkg/generated/external-snapshotter/client-go/clientset/versioned"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
)

var _ = Describe("Snapshot", func() {
	var tmpDir string
	var tenantClient *kubernetes.Clientset
	var tenantSnapshotClient *snapcli.Clientset
	var infraSnapClient *snapcli.Clientset
	var tenantAccessor *tenantClusterAccess
	var namespace string

	BeforeEach(func() {
		tmpDir, err := os.MkdirTemp(WorkingDir, "pvc-creation-tests")
		Expect(err).ToNot(HaveOccurred())
		namespace = "e2e-test-create-pvc-" + rand.String(6)

		tenantAccessor = createTenantAccessor(namespace, tmpDir)
		ns := &k8sv1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}

		tenantClient, err = tenantAccessor.generateTenantClient()
		Expect(err).ToNot(HaveOccurred())
		Expect(tenantClient).ToNot(BeNil())

		tenantSnapshotClient, err = tenantAccessor.generateTenantSnapshotClient()
		Expect(err).ToNot(HaveOccurred())
		Expect(tenantSnapshotClient).ToNot(BeNil())

		infraSnapClient, err = generateInfraSnapClient()
		Expect(err).ToNot(HaveOccurred())

		_, err = tenantClient.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		_ = tenantClient.CoreV1().Namespaces().Delete(context.Background(), namespace, metav1.DeleteOptions{})
		_ = tenantAccessor.stopForwardingTenantAPI()
		_ = os.RemoveAll(tmpDir)
	})

	DescribeTable("creates a pvc and attaches to pod, then create snapshot", Label("pvcCreation"), func(volumeMode k8sv1.PersistentVolumeMode, podCreationFunc, podReaderFunc func(string) *k8sv1.Pod) {
		pvcName := "test-pvc"
		storageClassName := "kubevirt"
		pvc := pvcSpec(pvcName, storageClassName, "10Mi")
		pvc.Spec.VolumeMode = &volumeMode

		By("creating a pvc")
		_, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("creating a pod that attaches pvc")
		runPod(
			tenantClient.CoreV1(),
			namespace,
			podCreationFunc(pvc.Name))

		By("creating a snapshot")
		snapshotName := "test-snapshot"
		snapshot, err := tenantSnapshotClient.SnapshotV1().VolumeSnapshots(namespace).Create(context.Background(), &snapshotv1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name: snapshotName,
			},
			Spec: snapshotv1.VolumeSnapshotSpec{
				Source: snapshotv1.VolumeSnapshotSource{
					PersistentVolumeClaimName: ptr.To[string](pvcName),
				},
				VolumeSnapshotClassName: &VolumeSnapshotClass,
			},
		}, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(snapshot).ToNot(BeNil())
		Eventually(func() bool {
			snapshot, err = tenantSnapshotClient.SnapshotV1().VolumeSnapshots(namespace).Get(context.Background(), snapshotName, metav1.GetOptions{})
			return err == nil && snapshot.Status != nil && snapshot.Status.ReadyToUse != nil && *snapshot.Status.ReadyToUse
		}, 30*time.Second, time.Second).Should(BeTrue())
		Expect(err).ToNot(HaveOccurred())
		Expect(snapshot).ToNot(BeNil())
		Expect(snapshot.Status).ToNot(BeNil())
		Expect(snapshot.Status.ReadyToUse).ToNot(BeNil())
		Expect(*snapshot.Status.ReadyToUse).To(BeTrue())
		Expect(snapshot.Status.RestoreSize).ToNot(BeNil())
		Expect(snapshot.Status.RestoreSize.Value()).To(Equal(int64(10 * 1024 * 1024)))

		By("Checking that the infra cluster has a snapshot matching the snapshot content name")
		snapshotContent, err := tenantSnapshotClient.SnapshotV1().VolumeSnapshotContents().Get(context.Background(), *snapshot.Status.BoundVolumeSnapshotContentName, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())

		infraSnapshot, err := infraSnapClient.SnapshotV1().VolumeSnapshots(InfraClusterNamespace).Get(context.Background(), *snapshotContent.Status.SnapshotHandle, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(infraSnapshot).ToNot(BeNil())
		Expect(infraSnapshot.Status).ToNot(BeNil())
		Expect(infraSnapshot.Status.ReadyToUse).ToNot(BeNil())
		Expect(*infraSnapshot.Status.ReadyToUse).To(BeTrue())

		By("creating a new PVC from the snapshot")
		pvc = pvcSpec(fmt.Sprintf("%s-restore", pvcName), storageClassName, "10Mi")
		pvc.Spec.VolumeMode = &volumeMode
		pvc.Spec.DataSourceRef = &k8sv1.TypedObjectReference{
			APIGroup: ptr.To[string]("snapshot.storage.k8s.io"),
			Kind:     "VolumeSnapshot",
			Name:     snapshotName,
		}
		_, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("creating a pod that attaches the restored pvc, and checks the changes are there")
		runPod(
			tenantClient.CoreV1(),
			namespace,
			podReaderFunc(pvc.Name))

	},
		Entry("Filesystem volume mode", k8sv1.PersistentVolumeFilesystem, writerPodFs, readerPodFs),
		Entry("Block volume mode", k8sv1.PersistentVolumeBlock, writerPodBlock, readerPodBlock),
	)
})
