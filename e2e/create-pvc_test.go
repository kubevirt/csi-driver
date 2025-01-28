package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	cdicli "kubevirt.io/csi-driver/pkg/generated/containerized-data-importer/client-go/clientset/versioned"
	kubecli "kubevirt.io/csi-driver/pkg/generated/kubevirt/client-go/clientset/versioned"
)

const hostNameLabelKey = "kubernetes.io/hostname"

var virtClient *kubecli.Clientset

func defaultInfraClientConfig(flags *pflag.FlagSet) clientcmd.ClientConfig {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.DefaultClientConfig = &clientcmd.DefaultClientConfig

	flags.StringVar(&loadingRules.ExplicitPath, "infra-kubeconfig", "", "Path to the kubeconfig file to use for CLI requests.")

	overrides := &clientcmd.ConfigOverrides{ClusterDefaults: clientcmd.ClusterDefaults}

	flagNames := clientcmd.RecommendedConfigOverrideFlags("")
	flagNames.ClusterOverrideFlags.APIServer.ShortName = "s"

	clientcmd.BindOverrideFlags(overrides, flags, flagNames)
	clientConfig := clientcmd.NewInteractiveDeferredLoadingClientConfig(loadingRules, overrides, os.Stdin)

	return clientConfig
}

var _ = Describe("CreatePVC", func() {

	var tmpDir string
	var tenantClient *kubernetes.Clientset
	var infraClient *kubernetes.Clientset
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

		infraClient, err = generateInfraClient()
		Expect(err).ToNot(HaveOccurred())

		_, err = tenantClient.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		_ = tenantClient.CoreV1().Namespaces().Delete(context.Background(), namespace, metav1.DeleteOptions{})
		_ = tenantAccessor.stopForwardingTenantAPI()
		_ = os.RemoveAll(tmpDir)
	})

	DescribeTable("creates a pvc and attaches to pod", Label("pvcCreation"), func(volumeMode k8sv1.PersistentVolumeMode, storageOpt storageOption, attachCmd string) {
		pvcName := "test-pvc"
		storageClassName := "kubevirt"
		pvc := pvcSpec(pvcName, storageClassName, "10Mi")
		pvc.Spec.VolumeMode = &volumeMode

		By("creating a pvc")
		_, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("creating a pod that attaches pvc")
		podSpec := createPod("test-pod",
			withCommand(attachCmd),
			storageOpt(pvc.Name))

		runPod(
			tenantClient.CoreV1(),
			namespace,
			podSpec,
			true)
	},
		Entry("Filesystem volume mode", Label("FS"), k8sv1.PersistentVolumeFilesystem, withFileSystem, fsAttachCommand),
		Entry("Block volume mode", Label("Block"), k8sv1.PersistentVolumeBlock, withBlock, blockAttachCommand),
	)

	It("should create a RW-Many block pvc and attaches to pod", Label("pvcCreation", "RWX", "Block"), func() {
		pvcName := "test-pvc"
		storageClassName := "kubevirt"
		pvc := pvcSpec(pvcName, storageClassName, "10Mi")
		pvc.Spec.VolumeMode = ptr.To(k8sv1.PersistentVolumeBlock)
		pvc.Spec.AccessModes = []k8sv1.PersistentVolumeAccessMode{k8sv1.ReadWriteMany}

		By("creating a pvc")
		_, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("creating a pod that attaches pvc")
		const (
			labelKey         = "app"
			writerLabelValue = "writer"
		)

		writerPod := runPod(
			tenantClient.CoreV1(),
			namespace,
			createPod("writer-pod",
				withBlock(pvc.Name),
				withCommand(blockWriteCommand+" && sleep 60"),
				withLabel(labelKey, writerLabelValue),
			),
			false,
		)

		GinkgoWriter.Printf("[DEBUG] writer pod node: %s\n", writerPod.Spec.NodeName)

		By("creating a different pod that reads from pvc")
		Eventually(func(g Gomega) {
			readerPod := runPod(
				tenantClient.CoreV1(),
				namespace,
				createPod("reader-pod",
					withCommand(blockReadCommand),
					withBlock(pvc.Name),
					withPodAntiAffinity(labelKey, writerLabelValue),
				),
				true,
			)

			defer deletePod(tenantClient.CoreV1(), namespace, readerPod.Name)
			GinkgoWriter.Printf("[DEBUG] reader pod node: %s\n", readerPod.Spec.NodeName)

			s := tenantClient.CoreV1().Pods(namespace).GetLogs(readerPod.Name, &k8sv1.PodLogOptions{})
			reader, err := s.Stream(context.Background())
			g.Expect(err).ToNot(HaveOccurred())
			defer reader.Close()
			buf := new(bytes.Buffer)
			n, err := buf.ReadFrom(reader)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(n).To(BeEquivalentTo(len("testing\n")))
			out := buf.String()
			g.Expect(strings.TrimSpace(out)).To(Equal("testing"))

		}).WithTimeout(120 * time.Second).WithPolling(20 * time.Second).Should(Succeed())

		deletePod(tenantClient.CoreV1(), namespace, writerPod.Name)
	})

	It("should creates a RW-Many block pvc, with FS infra storage class, and attaches to pod", Label("pvcCreation", "RWX", "Block", "infra-FS"), func() {
		pvcName := "test-pvc"
		storageClassName := "infra-fs"
		pvc := pvcSpec(pvcName, storageClassName, "10Mi")

		pvc.Spec.VolumeMode = ptr.To(k8sv1.PersistentVolumeBlock)
		pvc.Spec.AccessModes = []k8sv1.PersistentVolumeAccessMode{k8sv1.ReadWriteMany}

		By("creating a pvc")
		_, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("creating a pod that attaches pvc")
		const (
			labelKey         = "app"
			writerLabelValue = "writer"
		)

		writerPod := runPod(
			tenantClient.CoreV1(),
			namespace,
			createPod("writer-pod",
				withBlock(pvc.Name),
				withCommand(blockWriteCommand+" && sleep 60"),
				withLabel(labelKey, writerLabelValue),
			),
			false,
		)

		GinkgoWriter.Printf("[DEBUG] writer pod node: %s\n", writerPod.Spec.NodeName)

		By("creating a different pod that reads from pvc")
		Eventually(func(g Gomega) {
			readerPod := runPod(
				tenantClient.CoreV1(),
				namespace,
				createPod("reader-pod",
					withCommand(blockReadCommand),
					withBlock(pvc.Name),
					withPodAntiAffinity(labelKey, writerLabelValue),
				),
				true,
			)

			defer deletePod(tenantClient.CoreV1(), namespace, readerPod.Name)
			GinkgoWriter.Printf("[DEBUG] reader pod node: %s\n", readerPod.Spec.NodeName)

			s := tenantClient.CoreV1().Pods(namespace).GetLogs(readerPod.Name, &k8sv1.PodLogOptions{})
			reader, err := s.Stream(context.Background())
			g.Expect(err).ToNot(HaveOccurred())
			defer reader.Close()
			buf := new(bytes.Buffer)
			n, err := buf.ReadFrom(reader)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(n).To(BeEquivalentTo(len("testing\n")))
			out := buf.String()
			g.Expect(strings.TrimSpace(out)).To(Equal("testing"))

		}).WithTimeout(120 * time.Second).WithPolling(20 * time.Second).Should(Succeed())

		deletePod(tenantClient.CoreV1(), namespace, writerPod.Name)
	})

	It("should reject a RW-Many file-system pvc and attaches to pod", Label("pvcCreation", "RWX", "FS"), func() {
		const pvcName = "test-pvc"
		storageClassName := "kubevirt"
		pvc := pvcSpec(pvcName, storageClassName, "10Mi")

		pvc.Spec.VolumeMode = ptr.To(k8sv1.PersistentVolumeFilesystem)
		pvc.Spec.AccessModes = []k8sv1.PersistentVolumeAccessMode{k8sv1.ReadWriteMany}

		By("creating a pvc")
		_, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("creating a pod that attaches pvc")
		runPodAndExpectPending(
			tenantClient.CoreV1(),
			namespace,
			createPod("test-pod", withFileSystem(pvc.Name), withCommand(fsAttachCommand)))

		Eventually(func(g Gomega) bool {
			//Ensure we don't see couldn't find device by serial id in pod event log.
			events, err := tenantClient.CoreV1().Events(namespace).List(context.Background(), metav1.ListOptions{FieldSelector: fmt.Sprintf("involvedObject.name=%s", pvcName), TypeMeta: metav1.TypeMeta{Kind: "PersistentVolumeClaim"}})
			g.Expect(err).ToNot(HaveOccurred())

			foundError := false
			GinkgoWriter.Println("PVC Events:")
			for _, evt := range events.Items {
				GinkgoWriter.Println(evt.Message)
				if strings.Contains(evt.Message, "non-block volume with RWX access mode is not supported") {
					foundError = true
				}
			}

			return foundError
		}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(BeTrue())

	})

	DescribeTable("creates a pvc, attaches to pod, re-attach to another pod", Label("pvcCreation"), func(volumeMode k8sv1.PersistentVolumeMode, storageOpt storageOption, attachCmd string) {
		nodes, err := tenantClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
		Expect(err).ToNot(HaveOccurred())
		// select at least two node names
		if len(nodes.Items) < 2 {
			Skip("Can only run with 2 or more tenant nodes")
		}
		host1 := nodes.Items[0].Labels[hostNameLabelKey]
		host2 := nodes.Items[1].Labels[hostNameLabelKey]

		pvcName := "test-pvc"
		storageClassName := "kubevirt"
		pvc := pvcSpec(pvcName, storageClassName, "10Mi")
		pvc.Spec.VolumeMode = &volumeMode
		By("creating a pvc")
		_, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		podSpec := createPod("test-pod",
			storageOpt(pvc.Name),
			withCommand(attachCmd),
			withNodeSelector(hostNameLabelKey, host1))

		By(fmt.Sprintf("creating a pod that attaches pvc on node %s", host1))
		pod := runPod(tenantClient.CoreV1(), namespace, podSpec, true)
		deletePod(tenantClient.CoreV1(), namespace, pod.Name)

		pod.Spec.NodeSelector = map[string]string{hostNameLabelKey: host2}
		By(fmt.Sprintf("creating a pod that attaches pvc on node %s", host2))
		anotherPod := runPod(tenantClient.CoreV1(), namespace, podSpec, true)
		deletePod(tenantClient.CoreV1(), namespace, anotherPod.Name)
	},
		Entry("Filesystem volume mode", Label("FS"), k8sv1.PersistentVolumeFilesystem, withFileSystem, fsAttachCommand),
		Entry("Block volume mode", Label("Block"), k8sv1.PersistentVolumeBlock, withBlock, blockAttachCommand),
	)

	DescribeTable("verify persistence - creates a pvc, attaches to writer pod, re-attach to a reader pod", Label("pvcCreation"), func(volumeMode k8sv1.PersistentVolumeMode, storageOpt storageOption, writeCmd, readCmd string) {
		By("creating a pvc")
		pvc := pvcSpec("test-pvc", "kubevirt", "10Mi")
		pvc.Spec.VolumeMode = &volumeMode
		_, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("creating a pod that writes to pvc on node")
		rPod := createPod("writer-pod",
			withCommand(writeCmd),
			storageOpt(pvc.Name),
		)
		writerPod := runPod(tenantClient.CoreV1(), namespace, rPod, true)
		deletePod(tenantClient.CoreV1(), namespace, writerPod.Name)

		By("creating a different pod that reads from pvc")
		wPod := createPod("reader-pod",
			withCommand(readCmd),
			storageOpt(pvc.Name))
		readerPod := runPod(tenantClient.CoreV1(), namespace, wPod, true)
		s := tenantClient.CoreV1().Pods(namespace).GetLogs(readerPod.Name, &k8sv1.PodLogOptions{})
		reader, err := s.Stream(context.Background())
		Expect(err).ToNot(HaveOccurred())
		defer reader.Close()
		buf := new(bytes.Buffer)
		n, err := buf.ReadFrom(reader)
		Expect(err).ToNot(HaveOccurred())
		// testing\n
		Expect(n).To(Equal(int64(8)))
		out := buf.String()
		Expect(strings.TrimSpace(out)).To(Equal("testing"))
		deletePod(tenantClient.CoreV1(), namespace, readerPod.Name)
	},
		Entry("Filesystem volume mode", Label("FS"), k8sv1.PersistentVolumeFilesystem, withFileSystem, fsWriteCommand, fsReadCommand),
		Entry("Block volume mode", Label("Block"), k8sv1.PersistentVolumeBlock, withBlock, blockWriteCommand, blockReadCommand),
	)

	DescribeTable("multi attach - creates 3 pvcs, attach all 3 to pod, detach all 3 from the pod", Label("pvcCreation"), func(volumeMode k8sv1.PersistentVolumeMode, storageOpt storageOption, attachCmd string) {
		By("creating a pvc")
		pvc1 := pvcSpec("test-pvc1", "kubevirt", "10Mi")
		pvc1.Spec.VolumeMode = &volumeMode
		_, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc1, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
		pvc2 := pvcSpec("test-pvc2", "kubevirt", "10Mi")
		pvc2.Spec.VolumeMode = &volumeMode
		_, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc2, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
		pvc3 := pvcSpec("test-pvc3", "kubevirt", "10Mi")
		pvc3.Spec.VolumeMode = &volumeMode
		_, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc3, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("creating a pod that uses 3 PVCs")
		podSpec := createPod("test-pod",
			withCommand(attachCmd),
			storageOpt(pvc1.Name),
			withPVC(pvc2.Name, "/pv2"),
			withPVC(pvc3.Name, "/pv3"))

		pod := runPod(tenantClient.CoreV1(), namespace, podSpec, true)
		deletePod(tenantClient.CoreV1(), namespace, pod.Name)
	},
		Entry("Filesystem volume mode", Label("FS"), k8sv1.PersistentVolumeFilesystem, withFileSystem, fsAttachCommand),
		Entry("Block volume mode", Label("Block"), k8sv1.PersistentVolumeBlock, withBlock, blockAttachCommand),
	)

	DescribeTable("multi attach - create multiple pods pvcs on same node, and each pod should connect to a different PVC", Label("pvcCreation"), func(volumeMode k8sv1.PersistentVolumeMode, storageOpt storageOption, attachCmd string) {
		nodes, err := tenantClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
		Expect(err).ToNot(HaveOccurred())
		host := nodes.Items[0].Labels[hostNameLabelKey]

		pvcList := make([]*k8sv1.PersistentVolumeClaim, 0)
		for i := 0; i < 2; i++ {
			pvcName := fmt.Sprintf("test-pvc%d", i)
			storageClassName := "kubevirt"
			pvc := pvcSpec(pvcName, storageClassName, "10Mi")
			pvc.Spec.VolumeMode = &volumeMode
			By("creating a pvc")
			pvc, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			pvcList = append(pvcList, pvc)
		}

		podList := make([]*k8sv1.Pod, 0)
		for _, pvc := range pvcList {
			podSpec := createPod("test-pod",
				storageOpt(pvc.Name),
				withCommand(attachCmd),
				withNodeSelector(hostNameLabelKey, host))

			By(fmt.Sprintf("creating a pod that attaches pvc on node %s", host))
			pod := runPod(tenantClient.CoreV1(), namespace, podSpec, true)
			podList = append(podList, pod)
		}
		Eventually(func() bool {
			allCompleted := true
			for _, pod := range podList {
				pod, err = tenantClient.CoreV1().Pods(namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
				if err != nil {
					allCompleted = false
				} else {
					if pod.Status.Phase != k8sv1.PodSucceeded {
						allCompleted = false
					}
				}
			}
			return allCompleted
		}, time.Second*30, time.Second).Should(BeTrue())
		for _, pod := range podList {
			deletePod(tenantClient.CoreV1(), namespace, pod.Name)
		}
	},
		Entry("Filesystem volume mode", Label("FS"), k8sv1.PersistentVolumeFilesystem, withFileSystem, fsAttachCommand),
		Entry("Block volume mode", Label("Block"), k8sv1.PersistentVolumeBlock, withBlock, blockAttachCommand),
	)

	DescribeTable("Verify infra cluster cleanup", Label("pvc cleanup"), func(volumeMode k8sv1.PersistentVolumeMode, storageOpt storageOption, attachCmd string) {
		pvcName := "test-pvc"
		storageClassName := "kubevirt"
		pvc := pvcSpec(pvcName, storageClassName, "10Mi")
		pvc.Spec.VolumeMode = &volumeMode
		By("creating a pvc")
		pvc, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() k8sv1.PersistentVolumeClaimPhase {
			pvc, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Get(context.Background(), pvc.Name, metav1.GetOptions{})
			return pvc.Status.Phase
		}, time.Second*30, time.Second).Should(Equal(k8sv1.ClaimBound))
		volumeName := pvc.Spec.VolumeName

		podSpec := createPod("test-pod",
			storageOpt(pvc.Name),
			withCommand(attachCmd))

		pod := runPod(tenantClient.CoreV1(), namespace, podSpec, true)
		pod, err = tenantClient.CoreV1().Pods(namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(pod.Status.Phase).To(BeElementOf(k8sv1.PodSucceeded, k8sv1.PodRunning))

		Expect(findInfraPVC(infraClient, volumeName)).ToNot(BeNil())
		By("Deleting pod, the PVC should remain")
		deletePod(tenantClient.CoreV1(), namespace, pod.Name)
		By("Should still find infra PVC")
		infraPvc := findInfraPVC(infraClient, volumeName)
		Expect(infraPvc).ToNot(BeNil())

		err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Delete(context.Background(), pvc.Name, metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() bool {
			_, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Get(context.Background(), pvc.Name, metav1.GetOptions{})
			return errors.IsNotFound(err)
		}, 1*time.Minute, 2*time.Second).Should(BeTrue(), "tenant pvc should disappear")

		Eventually(func() bool {
			_, err := infraClient.CoreV1().PersistentVolumeClaims(InfraClusterNamespace).Get(context.Background(), infraPvc.Name, metav1.GetOptions{})
			return errors.IsNotFound(err)
		}, 1*time.Minute, 2*time.Second).Should(BeTrue(), "infra pvc should disappear")
	},
		Entry("Filesystem volume mode", Label("FS"), k8sv1.PersistentVolumeFilesystem, withFileSystem, fsAttachCommand),
		Entry("Block volume mode", Label("Block"), k8sv1.PersistentVolumeBlock, withBlock, blockAttachCommand),
	)

	Context("Should prevent access to volumes from infra cluster", func() {
		var tenantPVC *k8sv1.PersistentVolumeClaim
		var tenantPV *k8sv1.PersistentVolume
		var infraDV *cdiv1.DataVolume
		var infraCdiClient *cdicli.Clientset
		BeforeEach(func() {
			var err error
			infraCdiClient, err = generateInfraCdiClient()
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			By("Cleaning up resources for test")
			if tenantPVC != nil {
				err := tenantClient.CoreV1().PersistentVolumeClaims(tenantPVC.Namespace).Delete(context.Background(), tenantPVC.Name, metav1.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())
				Eventually(func() bool {
					_, err := tenantClient.CoreV1().PersistentVolumeClaims(tenantPVC.Namespace).Get(context.Background(), tenantPVC.Name, metav1.GetOptions{})
					return errors.IsNotFound(err)
				}, 1*time.Minute, 2*time.Second).Should(BeTrue(), "tenant pvc should disappear")
				tenantPVC = nil
			}
			// The tenant PV will not be able to be deleted because it can't find the matching
			// infra PV (since that is invalid).
			if infraDV != nil {
				_, err := infraCdiClient.CdiV1beta1().DataVolumes(InfraClusterNamespace).Get(context.Background(), infraDV.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				err = infraCdiClient.CdiV1beta1().DataVolumes(InfraClusterNamespace).Delete(context.Background(), infraDV.Name, metav1.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())
				infraDV = nil
			}
		})

		It("should not be able to create a PV and access a volume from the infra cluster that is not labeled", func() {
			infraDV = &cdiv1.DataVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "infra-pvc",
					Namespace: InfraClusterNamespace,
				},
				Spec: cdiv1.DataVolumeSpec{
					Source: &cdiv1.DataVolumeSource{
						Blank: &cdiv1.DataVolumeBlankImage{},
					},
					Storage: &cdiv1.StorageSpec{
						Resources: k8sv1.ResourceRequirements{
							Requests: k8sv1.ResourceList{
								k8sv1.ResourceStorage: resource.MustParse("1Gi"),
							},
						},
					},
				},
			}
			var err error
			infraDV, err = infraCdiClient.CdiV1beta1().DataVolumes(InfraClusterNamespace).Create(context.Background(), infraDV, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("Creating a specially crafted PV, attempt to access volume from infra cluster that should not be accessed")
			tenantPV = &k8sv1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name: "tenant-pv",
					Annotations: map[string]string{
						"pv.kubernetes.io/provisioned-by": "csi.kubevirt.io",
					},
				},
				Spec: k8sv1.PersistentVolumeSpec{
					AccessModes: []k8sv1.PersistentVolumeAccessMode{k8sv1.ReadWriteOnce},
					Capacity:    k8sv1.ResourceList{k8sv1.ResourceStorage: resource.MustParse("1Gi")},
					PersistentVolumeSource: k8sv1.PersistentVolumeSource{
						CSI: &k8sv1.CSIPersistentVolumeSource{
							Driver:       "csi.kubevirt.io",
							VolumeHandle: infraDV.Name,
							VolumeAttributes: map[string]string{
								"bus":    "scsi",
								"serial": "abcd",
								"storage.kubernetes.io/csiProvisionerIdentity": "1708112628060-923-csi.kubevirt.io",
							},
							FSType: "ext4",
						},
					},
					StorageClassName:              "kubevirt",
					PersistentVolumeReclaimPolicy: k8sv1.PersistentVolumeReclaimDelete,
				},
			}
			_, err = tenantClient.CoreV1().PersistentVolumes().Create(context.Background(), tenantPV, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			tenantPVC = &k8sv1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name: "tenant-pvc",
				},
				Spec: k8sv1.PersistentVolumeClaimSpec{
					AccessModes: []k8sv1.PersistentVolumeAccessMode{k8sv1.ReadWriteOnce},
					Resources: k8sv1.VolumeResourceRequirements{
						Requests: k8sv1.ResourceList{
							k8sv1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
					VolumeName: tenantPV.Name,
				},
			}
			tenantPVC, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), tenantPVC, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			pod := createPod("reader-pod", withFileSystem(tenantPVC.Name), withCommand(fsWriteCommand))

			By("Creating pod that attempts to use the specially crafted PVC")
			pod, err = tenantClient.CoreV1().Pods(namespace).Create(context.Background(), pod, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			defer deletePod(tenantClient.CoreV1(), namespace, pod.Name)

			involvedObject := fmt.Sprintf("involvedObject.name=%s", pod.Name)
			By("Waiting for error event to show up in pod event log")
			Eventually(func() bool {
				list, err := tenantClient.CoreV1().Events(namespace).List(context.Background(), metav1.ListOptions{
					FieldSelector: involvedObject, TypeMeta: metav1.TypeMeta{Kind: "Pod"},
				})
				Expect(err).ToNot(HaveOccurred())
				for _, event := range list.Items {
					klog.Infof("Event: %s [%s]", event.Message, event.Reason)
					if event.Reason == "FailedAttachVolume" && strings.Contains(event.Message, "invalid volume name") {
						return true
					}
				}
				return false
			}, 30*time.Second, time.Second).Should(BeTrue(), "error event should show up in pod event log")
		})
	})
})

func pvcSpec(pvcName, storageClassName, size string) *k8sv1.PersistentVolumeClaim {
	quantity, err := resource.ParseQuantity(size)
	Expect(err).ToNot(HaveOccurred())

	pvc := &k8sv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: pvcName},
		Spec: k8sv1.PersistentVolumeClaimSpec{
			AccessModes: []k8sv1.PersistentVolumeAccessMode{k8sv1.ReadWriteOnce},
			Resources: k8sv1.VolumeResourceRequirements{
				Requests: k8sv1.ResourceList{
					"storage": quantity,
				},
			},
			StorageClassName: &storageClassName,
		},
	}

	return pvc
}

func runPod(client v1.CoreV1Interface, namespace string, pod *k8sv1.Pod, waitComplete bool) *k8sv1.Pod {
	pod, err := client.Pods(namespace).Create(context.Background(), pod, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred())

	expectedPhase := k8sv1.PodSucceeded
	if !waitComplete {
		expectedPhase = k8sv1.PodRunning
	}
	By("Wait for pod to reach a completed phase")
	Eventually(func(g Gomega) k8sv1.PodPhase {
		pod, err = client.Pods(namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
		g.Expect(err).ToNot(HaveOccurred())
		return pod.Status.Phase
	}, 3*time.Minute, 5*time.Second).Should(Equal(expectedPhase), "Pod should reach Succeeded state")

	//Ensure we don't see couldn't find device by serial id in pod event log.
	events, err := client.Events(namespace).List(context.Background(), metav1.ListOptions{FieldSelector: fmt.Sprintf("involvedObject.name=%s", pod.Name), TypeMeta: metav1.TypeMeta{Kind: "Pod"}})
	Expect(err).ToNot(HaveOccurred())
	for _, event := range events.Items {
		Expect(event.Message).ToNot(ContainSubstring("find device by serial id"))
	}
	// Return updated pod
	return pod
}

func runPodAndExpectPending(client v1.CoreV1Interface, namespace string, pod *k8sv1.Pod) {
	pod, err := client.Pods(namespace).Create(context.Background(), pod, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred())

	Eventually(func(g Gomega) k8sv1.PodPhase {
		pod, err = client.Pods(namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
		g.Expect(err).ToNot(HaveOccurred())

		return pod.Status.Phase
	}).WithTimeout(60*time.Second).WithPolling(5*time.Second).Should(Equal(k8sv1.PodPending), "Pod should never reach Succeeded state")
}

func deletePod(client v1.CoreV1Interface, ns, podName string) {
	By("Delete pod")
	zero := int64(0)
	err := client.Pods(ns).Delete(context.Background(), podName,
		metav1.DeleteOptions{
			GracePeriodSeconds: &zero,
		})
	Expect(err).ToNot(HaveOccurred())

	By("verify deleted")
	Eventually(func() bool {
		_, err := client.Pods(ns).Get(context.Background(), podName, metav1.GetOptions{})
		return errors.IsNotFound(err)
	}, 3*time.Minute, 5*time.Second).Should(BeTrue(), "pod should disappear")
}

func findInfraPVC(infraClient *kubernetes.Clientset, volumeName string) *k8sv1.PersistentVolumeClaim {
	infraPvcList, err := infraClient.CoreV1().PersistentVolumeClaims(InfraClusterNamespace).List(context.Background(), metav1.ListOptions{})
	Expect(err).ToNot(HaveOccurred())
	Expect(infraPvcList.Items).ToNot(BeEmpty())
	for _, infraPvc := range infraPvcList.Items {
		// The infra PV volume name is part of the naming convention in the infra cluster
		if strings.Contains(infraPvc.Name, volumeName) {
			return &infraPvc
		}
	}
	return nil
}
