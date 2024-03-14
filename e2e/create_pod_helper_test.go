package e2e_test

import (
	"fmt"

	k8sv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	fsCommonFile    = "/opt/test.txt"
	fsWriteCommand  = "echo testing > " + fsCommonFile
	fsReadCommand   = "cat " + fsCommonFile
	fsAttachCommand = "ls -la /opt && echo kubevirt-csi-driver && mktemp /opt/test-XXXXXX"

	blockCommonFile    = "/dev/csi"
	blockWriteCommand  = "echo testing > " + blockCommonFile
	blockReadCommand   = "head -c 8 " + blockCommonFile
	blockAttachCommand = "ls -al /dev/csi"
)

type podOption func(pod *k8sv1.Pod)
type storageOption func(string) podOption

func createPod(podName string, opts ...podOption) *k8sv1.Pod {
	pod := &k8sv1.Pod{
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
					Name:  podName,
					Image: "busybox",
				},
			},
			// add toleration so we can use control node for tests
			Tolerations: []k8sv1.Toleration{
				{
					Key:      "node-role.kubernetes.io/master",
					Operator: k8sv1.TolerationOpExists,
					Effect:   k8sv1.TaintEffectNoSchedule,
				},
				{
					Key:      "node-role.kubernetes.io/control-plane",
					Operator: k8sv1.TolerationOpExists,
					Effect:   k8sv1.TaintEffectNoSchedule,
				},
			},
		},
	}

	for _, o := range opts {
		o(pod)
	}

	return pod
}

func withCommand(cmd string) podOption {
	return func(pod *k8sv1.Pod) {
		pod.Spec.Containers[0].Command = []string{"sh"}
		pod.Spec.Containers[0].Args = []string{"-c", cmd}
	}
}

func withBlock(pvcName string) podOption {
	const volumeName = "blockpv"
	return func(pod *k8sv1.Pod) {
		pod.Spec.Volumes = append(pod.Spec.Volumes, getVolume(volumeName, pvcName))
		pod.Spec.Containers[0].VolumeDevices = []k8sv1.VolumeDevice{
			{
				Name:       volumeName,
				DevicePath: "/dev/csi",
			},
		}
	}
}

func withFileSystem(pvcName string) podOption {
	const volumeName = "fspv"
	return func(pod *k8sv1.Pod) {
		pod.Spec.Volumes = append(pod.Spec.Volumes, getVolume(volumeName, pvcName))
		pod.Spec.Containers[0].VolumeMounts = []k8sv1.VolumeMount{
			{
				Name:      volumeName,
				MountPath: "/opt",
			},
		}
	}
}

func withNodeSelector(key, value string) podOption {
	return func(pod *k8sv1.Pod) {
		if pod.Spec.NodeSelector == nil {
			pod.Spec.NodeSelector = make(map[string]string)
		}
		pod.Spec.NodeSelector[key] = value
	}
}

func withLabel(key, value string) podOption {
	return func(pod *k8sv1.Pod) {
		if pod.Labels == nil {
			pod.Labels = make(map[string]string)
		}
		pod.Labels[key] = value
	}
}

func withPodAntiAffinity(key, value string) podOption {
	return func(pod *k8sv1.Pod) {
		pod.Spec.Affinity = &k8sv1.Affinity{
			PodAntiAffinity: &k8sv1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []k8sv1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      key,
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{value},
								},
							},
						},
						TopologyKey: hostNameLabelKey,
					},
				},
			},
		}
	}
}

func getVolume(volumeName, pvcName string) k8sv1.Volume {
	return k8sv1.Volume{
		Name: volumeName,
		VolumeSource: k8sv1.VolumeSource{
			PersistentVolumeClaim: &k8sv1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		},
	}
}

func withPVC(pvcName string, mountPath string) podOption {
	return func(pod *k8sv1.Pod) {
		pod.Spec.Volumes = append(pod.Spec.Volumes, getVolume(pvcName, pvcName))
		if len(pod.Spec.Containers[0].VolumeMounts) > 0 {
			addVolumeMount(pod, pvcName, mountPath)
		}
		if len(pod.Spec.Containers[0].VolumeDevices) > 0 {
			addVolumeDevice(pod, pvcName)
		}

	}
}

func addVolumeMount(podSpec *k8sv1.Pod, volumeName string, mountPath string) {
	podSpec.Spec.Containers[0].VolumeMounts = append(
		podSpec.Spec.Containers[0].VolumeMounts,
		k8sv1.VolumeMount{
			Name:      volumeName,
			MountPath: mountPath,
		})
}

func addVolumeDevice(podSpec *k8sv1.Pod, volumeName string) {
	podSpec.Spec.Containers[0].VolumeDevices = append(
		podSpec.Spec.Containers[0].VolumeDevices,
		k8sv1.VolumeDevice{
			Name:       volumeName,
			DevicePath: fmt.Sprintf("/dev/%s", volumeName),
		})
}
