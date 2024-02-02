package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	kubevirtv1 "kubevirt.io/api/core/v1"
	v1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	cdicli "kubevirt.io/csi-driver/pkg/generated/containerized-data-importer/client-go/clientset/versioned"
	snapcli "kubevirt.io/csi-driver/pkg/generated/external-snapshotter/client-go/clientset/versioned"
	kubecli "kubevirt.io/csi-driver/pkg/generated/kubevirt/client-go/clientset/versioned"
)

const (
	vmiSubresourceURL       = "/apis/subresources.kubevirt.io/%s/namespaces/%s/virtualmachineinstances/%s/%s"
	annDefaultSnapshotClass = "snapshot.storage.kubernetes.io/is-default-class"
)

//go:generate mockgen -source=./client.go -destination=./mock/client_generated.go -package=mock

// ClientBuilderFuncType is function type for building infra-cluster clients
type ClientBuilderFuncType func(kubeconfig string) (Client, error)

// Client is a wrapper object for actual infra-cluster clients: kubernetes and the kubevirt
type Client interface {
	Ping(ctx context.Context) error
	ListVirtualMachines(ctx context.Context, namespace string) ([]kubevirtv1.VirtualMachineInstance, error)
	GetVirtualMachine(ctx context.Context, namespace, name string) (*kubevirtv1.VirtualMachineInstance, error)
	DeleteDataVolume(ctx context.Context, namespace string, name string) error
	CreateDataVolume(ctx context.Context, namespace string, dataVolume *cdiv1.DataVolume) (*cdiv1.DataVolume, error)
	GetDataVolume(ctx context.Context, namespace string, name string) (*cdiv1.DataVolume, error)
	AddVolumeToVM(ctx context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.AddVolumeOptions) error
	RemoveVolumeFromVM(ctx context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.RemoveVolumeOptions) error
	EnsureVolumeAvailable(ctx context.Context, namespace, vmName, volumeName string, timeout time.Duration) error
	EnsureVolumeRemoved(ctx context.Context, namespace, vmName, volumeName string, timeout time.Duration) error
	EnsureSnapshotReady(ctx context.Context, namespace, name string, timeout time.Duration) error
	CreateVolumeSnapshot(ctx context.Context, namespace, name, claimName, snapshotClassName string) (*snapshotv1.VolumeSnapshot, error)
	GetVolumeSnapshot(ctx context.Context, namespace, name string) (*snapshotv1.VolumeSnapshot, error)
	DeleteVolumeSnapshot(ctx context.Context, namespace, name string) error
	ListVolumeSnapshots(ctx context.Context, namespace string) (*snapshotv1.VolumeSnapshotList, error)
}

type client struct {
	kubernetesClient *kubernetes.Clientset
	virtClient       *kubecli.Clientset
	cdiClient        *cdicli.Clientset
	snapClient       *snapcli.Clientset
	restClient       *rest.RESTClient
}

// NewClient New creates our client wrapper object for the actual kubeVirt and kubernetes clients we use.
func NewClient(config *rest.Config) (Client, error) {
	result := &client{}

	Scheme := runtime.NewScheme()
	Codecs := serializer.NewCodecFactory(Scheme)

	shallowCopy := *config
	shallowCopy.GroupVersion = &v1.StorageGroupVersion
	shallowCopy.NegotiatedSerializer = serializer.WithoutConversionCodecFactory{CodecFactory: Codecs}
	shallowCopy.APIPath = "/apis"
	shallowCopy.ContentType = runtime.ContentTypeJSON
	if config.UserAgent == "" {
		config.UserAgent = rest.DefaultKubernetesUserAgent()
	}

	restClient, err := rest.RESTClientFor(&shallowCopy)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	result.kubernetesClient = clientset
	kubevirtClient, err := kubecli.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	cdiClient, err := cdicli.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	snapClient, err := snapcli.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	result.virtClient = kubevirtClient
	result.cdiClient = cdiClient
	result.restClient = restClient
	result.snapClient = snapClient
	return result, nil
}

// AddVolumeToVM performs a hotplug of a DataVolume to a VM
func (c *client) AddVolumeToVM(ctx context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.AddVolumeOptions) error {
	uri := fmt.Sprintf(vmiSubresourceURL, v1.ApiStorageVersion, namespace, vmName, "addvolume")

	JSON, err := json.Marshal(hotPlugRequest)

	if err != nil {
		return err
	}

	return c.restClient.Put().AbsPath(uri).Body([]byte(JSON)).Do(ctx).Error()
}

// RemoveVolumeFromVM perform hotunplug of a DataVolume from a VM
func (c *client) RemoveVolumeFromVM(ctx context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.RemoveVolumeOptions) error {
	uri := fmt.Sprintf(vmiSubresourceURL, v1.ApiStorageVersion, namespace, vmName, "removevolume")

	JSON, err := json.Marshal(hotPlugRequest)

	if err != nil {
		return err
	}

	return c.restClient.Put().AbsPath(uri).Body([]byte(JSON)).Do(ctx).Error()
}

// EnsureVolumeAvailable checks to make sure the volume is available in the node before returning, checks for 2 minutes
func (c *client) EnsureVolumeAvailable(ctx context.Context, namespace, vmName, volumeName string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (done bool, err error) {
		vmi, err := c.GetVirtualMachine(ctx, namespace, vmName)
		if err != nil {
			return false, err
		}
		for _, volume := range vmi.Status.VolumeStatus {
			if volume.Name == volumeName && volume.Phase == kubevirtv1.VolumeReady {
				return true, nil
			}
		}
		// Have not found the ready hotplugged volume
		return false, nil
	})
}

// EnsureVolumeRemoved checks to make sure the volume is removed from the node before returning, checks for 2 minutes
func (c *client) EnsureVolumeRemoved(ctx context.Context, namespace, vmName, volumeName string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (done bool, err error) {
		vmi, err := c.GetVirtualMachine(ctx, namespace, vmName)
		if err != nil {
			return false, err
		}
		for _, volume := range vmi.Status.VolumeStatus {
			if volume.Name == volumeName {
				return false, nil
			}
		}
		// Have not found the hotplugged volume
		return true, nil
	})
}

// EnsureSnapshotReady checks to make sure the snapshot is ready before returning, checks for 2 minutes
func (c *client) EnsureSnapshotReady(ctx context.Context, namespace, name string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (done bool, err error) {
		snapshot, err := c.GetVolumeSnapshot(ctx, namespace, name)
		if err != nil {
			return false, err
		}
		if snapshot.Status != nil && snapshot.Status.ReadyToUse != nil {
			return *snapshot.Status.ReadyToUse, nil
		}
		return false, nil
	})
}

// ListVirtualMachines fetches a list of VMIs from the passed in namespace
func (c *client) ListVirtualMachines(ctx context.Context, namespace string) ([]kubevirtv1.VirtualMachineInstance, error) {
	list, err := c.virtClient.KubevirtV1().VirtualMachineInstances(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// GetVirtualMachine gets a VMIs from the passed in namespace
func (c *client) GetVirtualMachine(ctx context.Context, namespace, name string) (*kubevirtv1.VirtualMachineInstance, error) {
	return c.virtClient.KubevirtV1().VirtualMachineInstances(namespace).Get(ctx, name, metav1.GetOptions{})
}

// CreateDataVolume creates a new DataVolume under a namespace
func (c *client) CreateDataVolume(ctx context.Context, namespace string, dataVolume *cdiv1.DataVolume) (*cdiv1.DataVolume, error) {
	return c.cdiClient.CdiV1beta1().DataVolumes(namespace).Create(ctx, dataVolume, metav1.CreateOptions{})
}

// Ping performs a minimal request to the infra-cluster k8s api
func (c *client) Ping(ctx context.Context) error {
	_, err := c.kubernetesClient.ServerVersion()
	return err
}

// DeleteDataVolume deletes a DataVolume from a namespace by name
func (c *client) DeleteDataVolume(ctx context.Context, namespace string, name string) error {
	return c.cdiClient.CdiV1beta1().DataVolumes(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

func (c *client) GetDataVolume(ctx context.Context, namespace string, name string) (*cdiv1.DataVolume, error) {
	return c.cdiClient.CdiV1beta1().DataVolumes(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (c *client) CreateVolumeSnapshot(ctx context.Context, namespace, name, claimName, snapshotClassName string) (*snapshotv1.VolumeSnapshot, error) {
	snapshotClassName, err := c.getSnapshotClassNameFromVolumeName(ctx, namespace, claimName, snapshotClassName)
	if err != nil {
		return nil, err
	}
	snapshot := &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: snapshotv1.VolumeSnapshotSpec{
			Source: snapshotv1.VolumeSnapshotSource{
				PersistentVolumeClaimName: &claimName,
			},
		},
	}
	if snapshotClassName != "" {
		snapshot.Spec.VolumeSnapshotClassName = &snapshotClassName
	} else {
		return nil, fmt.Errorf("no snapshot class name found for snapshot %s, %#v", name, snapshot)
	}
	klog.V(5).Infof("Creating snapshot %s with snapshot class %s, %#v", name, snapshotClassName, snapshot)
	return c.snapClient.SnapshotV1().VolumeSnapshots(namespace).Create(ctx, snapshot, metav1.CreateOptions{})
}

func (c *client) getSnapshotClassNameFromVolumeName(ctx context.Context, namespace, claimName, snapshotClassName string) (string, error) {
	volumeName, err := c.getVolumeNameFromClaimName(ctx, namespace, claimName)
	if err != nil {
		return "", err
	} else if volumeName == "" {
		return "", fmt.Errorf("persistent volume claim :%s not bound", claimName)
	}
	storageClassName, err := c.getStorageClassFromVolume(ctx, volumeName)
	if err != nil {
		return "", err
	}
	snapshotClass, err := c.getSnapshotClassFromStorageClass(ctx, storageClassName, snapshotClassName)
	if err != nil {
		return "", err
	}
	return snapshotClass.Name, nil
}

// Determine the name of the volume associated with the passed in claim name
func (c *client) getVolumeNameFromClaimName(ctx context.Context, namespace, claimName string) (string, error) {
	volumeClaim, err := c.kubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, claimName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Error getting volume claim %s in namespace %s: %v", claimName, namespace, err)
		return "", err
	}
	klog.V(5).Infof("found volumeClaim %#v", volumeClaim)
	return volumeClaim.Spec.VolumeName, nil
}

// Determine the storage class from the volume
func (c *client) getStorageClassFromVolume(ctx context.Context, volumeName string) (string, error) {
	volume, err := c.kubernetesClient.CoreV1().PersistentVolumes().Get(ctx, volumeName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Error getting volume %s: %v", volumeName, err)
		return "", err
	}
	return volume.Spec.StorageClassName, nil
}

// Get the associated snapshot class based on the storage class the following logic is used:
// 1. If the snapshot class is provided AND the provisioner string matches, return that.
// 2. If the snapshot class is empty, find the snapshot classes associated with provisioner string.
// 3. Based on those snapshot classes use the one marked as default if set.
// 4. If no default is set return the first one.
func (c *client) getSnapshotClassFromStorageClass(ctx context.Context, storageClassName, volumeSnapshotClassName string) (*snapshotv1.VolumeSnapshotClass, error) {
	storageClass, err := c.kubernetesClient.StorageV1().StorageClasses().Get(ctx, storageClassName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Error getting storage class %s: %v", storageClassName, err)
		return nil, err
	}
	provisioner := storageClass.Provisioner
	snapshotClasses, err := c.snapClient.SnapshotV1().VolumeSnapshotClasses().List(ctx, metav1.ListOptions{})
	if errors.IsNotFound(err) {
		klog.V(5).Info("No snapshot classes found")
		return nil, nil
	} else if err != nil {
		klog.Errorf("Error getting snapshot classes: %v", err)
		return nil, err
	}
	var storageClassSnapshotClasses []snapshotv1.VolumeSnapshotClass
	for _, snapshotClass := range snapshotClasses.Items {
		if snapshotClass.Driver == provisioner {
			storageClassSnapshotClasses = append(storageClassSnapshotClasses, snapshotClass)
		}
	}

	var bestMatch *snapshotv1.VolumeSnapshotClass
	for i, snapshotClass := range storageClassSnapshotClasses {
		if i == 0 {
			bestMatch = &storageClassSnapshotClasses[i]
		}
		if snapshotClass.Name == volumeSnapshotClassName {
			return &snapshotClass, nil
		}
		ann := snapshotClass.GetAnnotations()
		if ann != nil && ann[annDefaultSnapshotClass] == "true" {
			bestMatch = &storageClassSnapshotClasses[i]
		}
	}
	return bestMatch, nil
}

func (c *client) GetVolumeSnapshot(ctx context.Context, namespace, name string) (*snapshotv1.VolumeSnapshot, error) {
	return c.snapClient.SnapshotV1().VolumeSnapshots(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (c *client) DeleteVolumeSnapshot(ctx context.Context, namespace, name string) error {
	return c.snapClient.SnapshotV1().VolumeSnapshots(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

func (c *client) ListVolumeSnapshots(ctx context.Context, namespace string) (*snapshotv1.VolumeSnapshotList, error) {
	return c.snapClient.SnapshotV1().VolumeSnapshots(namespace).List(ctx, metav1.ListOptions{})
}

func (c *client) GetVolumeSnapshotContent(ctx context.Context, name string) (*snapshotv1.VolumeSnapshotContent, error) {
	return c.snapClient.SnapshotV1().VolumeSnapshotContents().Get(ctx, name, metav1.GetOptions{})
}
