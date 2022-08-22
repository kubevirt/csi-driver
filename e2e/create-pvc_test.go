package e2e_test

import (
	"context"
	"fmt"
	"io/ioutil"
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

		volumeName := "pv1"
		image := "busybox"
		podName := "test-pod"
		pvcName := "test-pvc"
		cmd := []string{"sh"}
		args := []string{"-c", "while true; do ls -la /opt; echo this file system was made availble using kubevirt-csi-driver; mktmp /opt/test-XXXXXX; sleep 1m; done"}
		storageClassName := "kubevirt"

		quantity, err := resource.ParseQuantity("1Gi")
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

		pod := &k8sv1.Pod{
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
								ClaimName: pvc.GetName(),
							},
						},
					},
				},
			},
		}

		By("creating a pvc")
		_, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())

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
})
