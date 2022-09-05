package e2e_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"kubevirt.io/client-go/kubecli"
)

var virtClient kubecli.KubevirtClient

var _ = Describe("CreatePVC", func() {

	const hostNameLabelKey = "kubernetes.io/hostname"

	var tmpDir string
	var tenantClient *kubernetes.Clientset
	var tenantKubeconfigFile string
	var tenantAccessor tenantClusterAccess
	var namespace string

	BeforeEach(func() {
		var err error

		tmpDir, err = ioutil.TempDir(WorkingDir, "pvc-creation-tests")
		Expect(err).ToNot(HaveOccurred())

		tenantKubeconfigFile = filepath.Join(tmpDir, "tenant-kubeconfig.yaml")

		clientConfig := kubecli.DefaultClientConfig(&pflag.FlagSet{})
		virtClient, err = kubecli.GetKubevirtClientFromClientConfig(clientConfig)
		Expect(err).ToNot(HaveOccurred())

		tenantAccessor = newTenantClusterAccess("kvcluster", tenantKubeconfigFile)

		err = tenantAccessor.startForwardingTenantAPI()
		Expect(err).ToNot(HaveOccurred())

		tenantClient, err = tenantAccessor.generateClient()
		Expect(err).ToNot(HaveOccurred())

		namespace = "e2e-test-create-pvc-" + rand.String(6)
		ns := &k8sv1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}

		_, err = tenantClient.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		_ = tenantClient.CoreV1().Namespaces().Delete(context.Background(), namespace, metav1.DeleteOptions{})
		_ = tenantAccessor.stopForwardingTenantAPI()
		_ = os.RemoveAll(tmpDir)
	})

	It("creates a pvc and attaches to pod", Label("pvcCreation"), func() {
		pvcName := "test-pvc"
		storageClassName := "kubevirt"
		pvc := pvcSpec(pvcName, storageClassName, "1Gi")

		By("creating a pvc")
		_, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("creating a pod that attaches pvc")
		runPod(
			tenantClient.CoreV1(),
			namespace,
			attacherPod(pvc.Name))
	})
	//       kubernetes.io/hostname: kvcluster-control-plane-z6jcv

	It("creates a pvc, attaches to pod, re-attach to another pod", Label("pvcCreation"), func() {
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
		pvc := pvcSpec(pvcName, storageClassName, "1Gi")
		By("creating a pvc")
		_, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		podSpec := attacherPod(pvc.Name)
		podSpec.Spec.NodeSelector = map[string]string{hostNameLabelKey: host1}

		By(fmt.Sprintf("creating a pod that attaches pvc on node %s", host1))
		pod := runPod(tenantClient.CoreV1(), namespace, podSpec)
		deletePod(tenantClient.CoreV1(), namespace, pod.Name)

		pod.Spec.NodeSelector = map[string]string{hostNameLabelKey: host2}
		By(fmt.Sprintf("creating a pod that attaches pvc on node %s", host2))
		anotherPod := runPod(tenantClient.CoreV1(), namespace, podSpec)
		deletePod(tenantClient.CoreV1(), namespace, anotherPod.Name)
	})

	It("verify persistence - creates a pvc, attaches to writer pod, re-attach to a reader pod", Label("pvcCreation"), func() {
		By("creating a pvc")
		pvc := pvcSpec("test-pvc", "kubevirt", "1Gi")
		_, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("creating a pod that writes to pvc on node")
		writerPod := runPod(tenantClient.CoreV1(), namespace, writerPod(pvc.Name))
		deletePod(tenantClient.CoreV1(), namespace, writerPod.Name)

		By("creating a different pod that reads from pvc")
		readerPod := runPod(tenantClient.CoreV1(), namespace, readerPod(pvc.Name))
		deletePod(tenantClient.CoreV1(), namespace, readerPod.Name)
	})
})

func writerPod(volumeName string) *k8sv1.Pod {
	return podWithPvcSpec("writer-pod",
		volumeName,
		[]string{"sh"},
		[]string{"-c", "echo testing > /opt/test.txt && sleep 1s"})
}

func readerPod(volumeName string) *k8sv1.Pod {
	return podWithPvcSpec("reader-pod",
		volumeName,
		[]string{"sh"},
		[]string{"-c", "cat /opt/test.txt"})
}

func attacherPod(pvcName string) *k8sv1.Pod {
	return podWithPvcSpec("test-pod",
		pvcName,
		[]string{"sh"},
		[]string{"-c", "while true; do ls -la /opt; echo this file system was made availble using kubevirt-csi-driver; mktmp /opt/test-XXXXXX; sleep 1m; done"})
}

func podWithPvcSpec(podName, pvcName string, cmd, args []string) *k8sv1.Pod {
	image := "busybox"
	volumeName := "pv1"

	return &k8sv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: podName,
		},
		Spec: k8sv1.PodSpec{
			RestartPolicy: k8sv1.RestartPolicyNever,
			Containers: []k8sv1.Container{
				{
					Name:    podName,
					Image:   image,
					Command: cmd,
					Args:    args,
					VolumeMounts: []k8sv1.VolumeMount{
						{
							Name:      volumeName,
							MountPath: "/opt",
						},
					},
				},
			},
			Volumes: []k8sv1.Volume{
				{
					Name: volumeName,
					VolumeSource: k8sv1.VolumeSource{
						PersistentVolumeClaim: &k8sv1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
			// add toleration so we can use control node for tests
			Tolerations: []k8sv1.Toleration{{
				Key:      "node-role.kubernetes.io/master",
				Operator: k8sv1.TolerationOpExists,
				Effect:   k8sv1.TaintEffectNoSchedule,
			}},
		},
	}
}

func pvcSpec(pvcName, storageClassName, size string) *k8sv1.PersistentVolumeClaim {
	quantity, err := resource.ParseQuantity(size)
	Expect(err).ToNot(HaveOccurred())

	pvc := &k8sv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: pvcName},
		Spec: k8sv1.PersistentVolumeClaimSpec{
			AccessModes: []k8sv1.PersistentVolumeAccessMode{k8sv1.ReadWriteOnce},
			Resources: k8sv1.ResourceRequirements{
				Requests: k8sv1.ResourceList{
					"storage": quantity,
				},
			},
			StorageClassName: &storageClassName,
		},
	}

	return pvc
}

func runPod(client v1.CoreV1Interface, namespace string, pod *k8sv1.Pod) *k8sv1.Pod {
	newPod, err := client.Pods(namespace).Create(context.Background(), pod, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred())

	By("Wait for pod to reach a completed phase")
	Eventually(func() error {
		updatedPod, err := client.Pods(namespace).Get(context.Background(), newPod.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if updatedPod.Status.Phase != k8sv1.PodRunning {
			return fmt.Errorf("Pod in phase %s, expected Running", updatedPod.Status.Phase)
		}
		return nil
	}, 3*time.Minute, 5*time.Second).Should(Succeed(), "pod should reach running state")

	return newPod
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
		if errors.IsNotFound(err) {
			return true
		}
		return false
	}, 3*time.Minute, 5*time.Second).Should(BeTrue(), "pod should disappear")
}
