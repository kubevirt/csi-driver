package e2e_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"

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
	var infraClient *kubernetes.Clientset
	var tenantKubeconfigFile string
	var tenantAccessor tenantClusterAccess
	var namespace string

	BeforeEach(func() {
		var err error

		if len(TenantKubeConfig) == 0 {
			tmpDir, err = ioutil.TempDir(WorkingDir, "pvc-creation-tests")
			Expect(err).ToNot(HaveOccurred())

			tenantKubeconfigFile = filepath.Join(tmpDir, "tenant-kubeconfig.yaml")

			clientConfig := kubecli.DefaultClientConfig(&pflag.FlagSet{})
			virtClient, err = kubecli.GetKubevirtClientFromClientConfig(clientConfig)
			Expect(err).ToNot(HaveOccurred())

			tenantAccessor = newTenantClusterAccess("kvcluster", tenantKubeconfigFile)

			err = tenantAccessor.startForwardingTenantAPI()
			Expect(err).ToNot(HaveOccurred())
		} else {
			tenantAccessor = newTenantClusterAccess(InfraClusterNamespace, TenantKubeConfig)
		}
		tenantClient, err = tenantAccessor.generateClient()
		Expect(err).ToNot(HaveOccurred())

		namespace = "e2e-test-create-pvc-" + rand.String(6)
		ns := &k8sv1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}

		infraClient, err = generateInfraClient()
		Expect(err).ToNot(HaveOccurred())

		_, err = tenantClient.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		_ = tenantClient.CoreV1().Namespaces().Delete(context.Background(), namespace, metav1.DeleteOptions{})
		if len(TenantKubeConfig) == 0 {
			_ = tenantAccessor.stopForwardingTenantAPI()
		}
		_ = os.RemoveAll(tmpDir)
	})

	Context("Filesystem volume mode", func() {
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
				attacherPodFs(pvc.Name))
		})

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

			podSpec := attacherPodFs(pvc.Name)
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

		It("multi attach - creates 3 pvcs, attach all 3 to pod, detach all 3 from the pod", Label("pvcCreation"), func() {
			By("creating a pvc")
			pvc1 := pvcSpec("test-pvc1", "kubevirt", "1Gi")
			_, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc1, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			pvc2 := pvcSpec("test-pvc2", "kubevirt", "1Gi")
			_, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc2, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			pvc3 := pvcSpec("test-pvc3", "kubevirt", "1Gi")
			_, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc3, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("creating a pod that uses 3 PVCs")
			podSpec := attacherPodFs(pvc1.Name)
			addPvc(podSpec, pvc2.Name, "/pv2")
			addPvc(podSpec, pvc3.Name, "/pv3")

			pod := runPod(tenantClient.CoreV1(), namespace, podSpec)
			deletePod(tenantClient.CoreV1(), namespace, pod.Name)
		})

		It("multi attach - create multiple pods pvcs on same node, and each pod should connect to a different PVC", Label("pvcCreation"), func() {
			nodes, err := tenantClient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			host := nodes.Items[0].Labels[hostNameLabelKey]

			pvcList := make([]*k8sv1.PersistentVolumeClaim, 0)
			for i := 0; i < 2; i++ {
				pvcName := fmt.Sprintf("test-pvc%d", i)
				storageClassName := "kubevirt"
				pvc := pvcSpec(pvcName, storageClassName, "10Mi")
				By("creating a pvc")
				pvc, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred())
				pvcList = append(pvcList, pvc)
			}

			podList := make([]*k8sv1.Pod, 0)
			for _, pvc := range pvcList {
				podSpec := attacherPodFs(pvc.Name)
				podSpec.Spec.NodeSelector = map[string]string{hostNameLabelKey: host}

				By(fmt.Sprintf("creating a pod that attaches pvc on node %s", host))
				pod := runPod(tenantClient.CoreV1(), namespace, podSpec)
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
		})

		It("Verify infra cluster cleanup", Label("pvc cleanup"), func() {
			pvcName := "test-pvc"
			storageClassName := "kubevirt"
			pvc := pvcSpec(pvcName, storageClassName, "10Mi")
			By("creating a pvc")
			pvc, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			Eventually(func() k8sv1.PersistentVolumeClaimPhase {
				pvc, err = tenantClient.CoreV1().PersistentVolumeClaims(namespace).Get(context.Background(), pvc.Name, metav1.GetOptions{})
				return pvc.Status.Phase
			}, time.Second*30, time.Second).Should(Equal(k8sv1.ClaimBound))
			volumeName := pvc.Spec.VolumeName

			podSpec := attacherPodFs(pvc.Name)
			pod := runPod(tenantClient.CoreV1(), namespace, podSpec)
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
		})
	})

	Context("Block volume mode", func() {
		It("creates a block pvc and attaches to pod", Label("block pvcCreation"), func() {
			blockMode := k8sv1.PersistentVolumeBlock
			pvcName := "test-pvc"
			storageClassName := "kubevirt"
			pvc := pvcSpec(pvcName, storageClassName, "1Gi")
			pvc.Spec.VolumeMode = &blockMode

			By("creating a pvc")
			_, err := tenantClient.CoreV1().PersistentVolumeClaims(namespace).Create(context.Background(), pvc, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("creating a pod that attaches pvc")
			runPod(
				tenantClient.CoreV1(),
				namespace,
				attacherPodBlock(pvc.Name))
		})
	})
})

func writerPod(volumeName string) *k8sv1.Pod {
	return podWithFilesystemPvcSpec("writer-pod",
		volumeName,
		[]string{"sh"},
		[]string{"-c", "echo testing > /opt/test.txt"})
}

func readerPod(pvcName string) *k8sv1.Pod {
	return podWithFilesystemPvcSpec("reader-pod",
		pvcName,
		[]string{"sh"},
		[]string{"-c", "cat /opt/test.txt"})
}

func attacherPodFs(pvcName string) *k8sv1.Pod {
	return podWithFilesystemPvcSpec("test-pod",
		pvcName,
		[]string{"sh"},
		[]string{"-c", "ls -la /opt && echo kubevirt-csi-driver && mktemp /opt/test-XXXXXX"})
}

func attacherPodBlock(pvcName string) *k8sv1.Pod {
	return podWithBlockPvcSpec("test-pod",
		pvcName,
		[]string{"sh"},
		[]string{"-c", "ls -al /dev/csi"})
}

func podWithoutPVCSpec(podName string, cmd, args []string) *k8sv1.Pod {
	image := "busybox"
	return &k8sv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: podName,
		},
		Spec: k8sv1.PodSpec{
			SecurityContext: &k8sv1.PodSecurityContext{
				SeccompProfile: &k8sv1.SeccompProfile{
					Type: k8sv1.SeccompProfileTypeRuntimeDefault,
				},
			},
			RestartPolicy: k8sv1.RestartPolicyNever,
			Containers: []k8sv1.Container{
				{
					SecurityContext: &k8sv1.SecurityContext{
						Capabilities: &k8sv1.Capabilities{
							Drop: []k8sv1.Capability{
								"ALL",
							},
						},
					},
					Name:    podName,
					Image:   image,
					Command: cmd,
					Args:    args,
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

func podWithBlockPvcSpec(podName, pvcName string, cmd, args []string) *k8sv1.Pod {
	podSpec := podWithoutPVCSpec(podName, cmd, args)
	volumeName := "blockpv"
	podSpec.Spec.Volumes = append(podSpec.Spec.Volumes, k8sv1.Volume{
		Name: volumeName,
		VolumeSource: k8sv1.VolumeSource{
			PersistentVolumeClaim: &k8sv1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		},
	})
	podSpec.Spec.Containers[0].VolumeDevices = []k8sv1.VolumeDevice{
		{
			Name:       volumeName,
			DevicePath: "/dev/csi",
		},
	}
	return podSpec
}

func podWithFilesystemPvcSpec(podName, pvcName string, cmd, args []string) *k8sv1.Pod {
	podSpec := podWithoutPVCSpec(podName, cmd, args)
	volumeName := "fspv"
	podSpec.Spec.Volumes = append(podSpec.Spec.Volumes, k8sv1.Volume{
		Name: volumeName,
		VolumeSource: k8sv1.VolumeSource{
			PersistentVolumeClaim: &k8sv1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		},
	})
	podSpec.Spec.Containers[0].VolumeMounts = []k8sv1.VolumeMount{
		{
			Name:      volumeName,
			MountPath: "/opt",
		},
	}
	return podSpec
}

func addPvc(podSpec *k8sv1.Pod, pvcName string, mountPath string) *k8sv1.Pod {
	volumeName := pvcName
	podSpec.Spec.Volumes = append(
		podSpec.Spec.Volumes,
		k8sv1.Volume{
			Name: volumeName,
			VolumeSource: k8sv1.VolumeSource{
				PersistentVolumeClaim: &k8sv1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName,
				},
			},
		})

	podSpec.Spec.Containers[0].VolumeMounts = append(
		podSpec.Spec.Containers[0].VolumeMounts,
		k8sv1.VolumeMount{
			Name:      volumeName,
			MountPath: mountPath,
		})

	return podSpec
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
		// TODO: change command and wait for completed/succeeded
		if updatedPod.Status.Phase != k8sv1.PodSucceeded {
			return fmt.Errorf("Pod in phase %s, expected Succeeded", updatedPod.Status.Phase)
		}
		return nil
	}, 3*time.Minute, 5*time.Second).Should(Succeed(), "Pod should reach Succeeded state")
	//Ensure we don't see couldn't find device by serial id in pod event log.
	events, err := client.Events(namespace).List(context.Background(), metav1.ListOptions{FieldSelector: fmt.Sprintf("involvedObject.name=%s", newPod.Name), TypeMeta: metav1.TypeMeta{Kind: "Pod"}})
	Expect(err).ToNot(HaveOccurred())
	for _, event := range events.Items {
		Expect(event.Message).ToNot(ContainSubstring("find device by serial id"))
	}
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
