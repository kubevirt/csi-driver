package e2e_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/api/errors"
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

		podName := "test-pod"
		cmd := []string{"sh"}
		args := []string{"-c", "while true; do ls -la /opt; echo this file system was made availble using kubevirt-csi-driver; mktmp /opt/test-XXXXXX; sleep 1m; done"}

		pvc := pvcSpec(pvcName, storageClassName, "1Gi")

		By("creating a pvc")
		_, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		pod := podWithPvcSpec(podName, pvcName, cmd, args)
		By("creating a pod that attaches pvc")
		newPod, err := tenantClient.CoreV1().Pods(namespace).Create(context.Background(), pod, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("Wait for pod to reach a running phase")
		Eventually(func() error {
			updatedPod, err := tenantClient.CoreV1().Pods(namespace).Get(context.Background(), newPod.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if updatedPod.Status.Phase != k8sv1.PodRunning {
				return fmt.Errorf("Pod in phase %s, expected Running", updatedPod.Status.Phase)
			}
			return nil
		}, 3*time.Minute, 5*time.Second).Should(Succeed(), "pod should reach running state")

	})
	//       kubernetes.io/hostname: kvcluster-control-plane-z6jcv

	It("creates a pvc and attaches to pod", Label("pvcCreation"), func() {
		pvcName := "test-pvc"
		storageClassName := "kubevirt"

		podName := "test-pod"
		cmd := []string{"sh"}
		args := []string{"-c", "while true; do ls -la /opt; echo this file system was made availble using kubevirt-csi-driver; mktmp /opt/test-XXXXXX; sleep 1m; done"}

		nodes, err := tenantClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
		Expect(err).ToNot(HaveOccurred())
		// select at least two node names
		if len(nodes.Items) < 2 {
			Skip("Can only run with 2 or more tenant nodes")
		}
		host1 := nodes.Items[0].Labels[hostNameLabelKey]
		host2 := nodes.Items[1].Labels[hostNameLabelKey]

		pvc := pvcSpec(pvcName, storageClassName, "1Gi")
		By("creating a pvc")
		_, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		pod := podWithPvcSpec(podName, pvcName, cmd, args)
		// add toleration so we can use control node for tests
		pod.Spec.Tolerations = []k8sv1.Toleration{{
			Key:      "node-role.kubernetes.io/master",
			Operator: k8sv1.TolerationOpExists,
			Effect:   k8sv1.TaintEffectNoSchedule,
		}}
		pod.Spec.NodeSelector = map[string]string{hostNameLabelKey: host1}

		By(fmt.Sprintf("creating a pod that attaches pvc on node %s", host1))
		newPod, err := tenantClient.CoreV1().Pods(namespace).Create(context.Background(), pod, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("Wait for pod to reach a running phase")
		Eventually(func() error {
			updatedPod, err := tenantClient.CoreV1().Pods(namespace).Get(context.Background(), newPod.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if updatedPod.Status.Phase != k8sv1.PodRunning {
				return fmt.Errorf("Pod in phase %s, expected Running", updatedPod.Status.Phase)
			}
			return nil
		}, 3*time.Minute, 5*time.Second).Should(Succeed(), "pod should reach running state")

		err = tenantClient.CoreV1().Pods(namespace).Delete(context.Background(), newPod.Name, metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() bool {
			_, err := tenantClient.CoreV1().Pods(namespace).Get(context.Background(), newPod.Name, metav1.GetOptions{})
			if errors.IsNotFound(err) {
				return true
			}
			return false
		}, 3*time.Minute, 5*time.Second).Should(BeTrue(), "pod should disappear")

		pod.Spec.NodeSelector = map[string]string{hostNameLabelKey: host2}
		By(fmt.Sprintf("creating a pod that attaches pvc on node %s", host2))
		newPod, err = tenantClient.CoreV1().Pods(namespace).Create(context.Background(), pod, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

		By("Wait for pod to reach a running phase")
		Eventually(func() error {
			updatedPod, err := tenantClient.CoreV1().Pods(namespace).Get(context.Background(), newPod.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			if updatedPod.Status.Phase != k8sv1.PodRunning {
				return fmt.Errorf("Pod in phase %s, expected Running", updatedPod.Status.Phase)
			}
			return nil
		}, 3*time.Minute, 5*time.Second).Should(Succeed(), "pod should reach running state")

	})
})

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
