package kubevirt

import (
	"context"
	"encoding/json"
	goerrors "errors"
	"fmt"
	"strings"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	cdicli "kubevirt.io/csi-driver/pkg/generated/containerized-data-importer/client-go/clientset/versioned"
	snapcli "kubevirt.io/csi-driver/pkg/generated/external-snapshotter/client-go/clientset/versioned"
	kubecli "kubevirt.io/csi-driver/pkg/generated/kubevirt/client-go/clientset/versioned"
	"kubevirt.io/csi-driver/pkg/util"
)

const (
	vmiSubresourceURL               = "/apis/subresources.kubevirt.io/%s/namespaces/%s/virtualmachineinstances/%s/%s"
	annDefaultSnapshotClass         = "snapshot.storage.kubernetes.io/is-default-class"
	InfraStorageClassNameParameter  = "infraStorageClassName"
	InfraSnapshotClassNameParameter = "infraSnapshotClassName"
)

type InfraTenantStorageSnapshotMapping struct {
	VolumeSnapshotClasses []InfraToTenantMapping
	StorageClasses        []string
}

type InfraToTenantMapping struct {
	Infra  string
	Tenant string
}

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
	infraKubernetesClient             kubernetes.Interface
	tenantKubernetesClient            kubernetes.Interface
	virtClient                        kubecli.Interface
	cdiClient                         cdicli.Interface
	infraSnapClient                   snapcli.Interface
	restClient                        *rest.RESTClient
	storageClassEnforcement           util.StorageClassEnforcement
	infraLabelMap                     map[string]string
	volumePrefix                      string
	infraTenantStorageSnapshotMapping []InfraTenantStorageSnapshotMapping
}

// NewClient New creates our client wrapper object for the actual kubeVirt and kubernetes clients we use.
func NewClient(infraConfig *rest.Config, infraClusterLabelMap map[string]string, tenantKubernetesClient kubernetes.Interface, tenantSnapshotClient snapcli.Interface, storageClassEnforcement util.StorageClassEnforcement, prefix string) (Client, error) {
	result := &client{}

	Scheme := runtime.NewScheme()
	Codecs := serializer.NewCodecFactory(Scheme)

	shallowCopy := *infraConfig
	shallowCopy.GroupVersion = &kubevirtv1.StorageGroupVersion
	shallowCopy.NegotiatedSerializer = serializer.WithoutConversionCodecFactory{CodecFactory: Codecs}
	shallowCopy.APIPath = "/apis"
	shallowCopy.ContentType = runtime.ContentTypeJSON
	if infraConfig.UserAgent == "" {
		infraConfig.UserAgent = rest.DefaultKubernetesUserAgent()
	}

	restClient, err := rest.RESTClientFor(&shallowCopy)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(infraConfig)
	if err != nil {
		return nil, err
	}
	result.infraKubernetesClient = clientset
	kubevirtClient, err := kubecli.NewForConfig(infraConfig)
	if err != nil {
		return nil, err
	}
	cdiClient, err := cdicli.NewForConfig(infraConfig)
	if err != nil {
		return nil, err
	}
	snapClient, err := snapcli.NewForConfig(infraConfig)
	if err != nil {
		return nil, err
	}

	result.virtClient = kubevirtClient
	result.cdiClient = cdiClient
	result.restClient = restClient
	result.infraSnapClient = snapClient
	result.infraLabelMap = infraClusterLabelMap
	result.volumePrefix = fmt.Sprintf("%s-", prefix)
	result.storageClassEnforcement = storageClassEnforcement
	result.tenantKubernetesClient = tenantKubernetesClient
	storageSnapshotMapping, err := result.buildStorageClassSnapshotClassMapping(tenantKubernetesClient, tenantSnapshotClient, storageClassEnforcement.StorageSnapshotMapping)
	if err != nil {
		return nil, err
	}
	result.infraTenantStorageSnapshotMapping = storageSnapshotMapping
	return result, nil
}

func containsLabels(a, b map[string]string) bool {
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

// AddVolumeToVM performs a hotplug of a DataVolume to a VM
func (c *client) AddVolumeToVM(ctx context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.AddVolumeOptions) error {
	uri := fmt.Sprintf(vmiSubresourceURL, kubevirtv1.ApiStorageVersion, namespace, vmName, "addvolume")

	JSON, err := json.Marshal(hotPlugRequest)

	if err != nil {
		return err
	}

	return c.restClient.Put().AbsPath(uri).Body([]byte(JSON)).Do(ctx).Error()
}

// RemoveVolumeFromVM perform hotunplug of a DataVolume from a VM
func (c *client) RemoveVolumeFromVM(ctx context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.RemoveVolumeOptions) error {
	uri := fmt.Sprintf(vmiSubresourceURL, kubevirtv1.ApiStorageVersion, namespace, vmName, "removevolume")

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
	if !strings.HasPrefix(dataVolume.GetName(), c.volumePrefix) {
		return nil, ErrInvalidVolume
	} else {
		return c.cdiClient.CdiV1beta1().DataVolumes(namespace).Create(ctx, dataVolume, metav1.CreateOptions{})
	}
}

// Ping performs a minimal request to the infra-cluster k8s api
func (c *client) Ping(ctx context.Context) error {
	_, err := c.infraKubernetesClient.Discovery().ServerVersion()
	return err
}

// DeleteDataVolume deletes a DataVolume from a namespace by name
func (c *client) DeleteDataVolume(ctx context.Context, namespace string, name string) error {
	if dv, err := c.GetDataVolume(ctx, namespace, name); errors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	} else if dv != nil {
		return c.cdiClient.CdiV1beta1().DataVolumes(namespace).Delete(ctx, dv.Name, metav1.DeleteOptions{})
	}
	return nil
}

func (c *client) GetDataVolume(ctx context.Context, namespace string, name string) (*cdiv1.DataVolume, error) {
	dv, err := c.cdiClient.CdiV1beta1().DataVolumes(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if dv != nil {
		if !containsLabels(dv.Labels, c.infraLabelMap) || !strings.HasPrefix(dv.GetName(), c.volumePrefix) {
			return nil, ErrInvalidVolume
		}
	}
	return dv, nil
}

func (c *client) CreateVolumeSnapshot(ctx context.Context, namespace, name, claimName, snapshotClassName string) (*snapshotv1.VolumeSnapshot, error) {
	if dv, err := c.GetDataVolume(ctx, namespace, claimName); err != nil {
		return nil, err
	} else {
		snapshotClassNameFromStorage, err := c.getSnapshotClassNameFromVolumeClaimName(ctx, namespace, dv.GetName(), snapshotClassName)
		if err != nil {
			return nil, err
		}
		snapshot := &snapshotv1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    c.infraLabelMap,
			},
			Spec: snapshotv1.VolumeSnapshotSpec{
				Source: snapshotv1.VolumeSnapshotSource{
					PersistentVolumeClaimName: &claimName,
				},
			},
		}
		// If the snapshot class is not found (blank), use 'default snapshost class' for infra cluster
		// that is associated with the storage class provider.
		if snapshotClassNameFromStorage != "" {
			snapshot.Spec.VolumeSnapshotClassName = &snapshotClassNameFromStorage
		}
		klog.V(5).Infof("Creating snapshot %s with snapshot class [%s], %#v", name, snapshotClassName, snapshot)
		return c.infraSnapClient.SnapshotV1().VolumeSnapshots(namespace).Create(ctx, snapshot, metav1.CreateOptions{})
	}
}

func (c *client) getSnapshotClassNameFromVolumeClaimName(ctx context.Context, namespace, claimName, snapshotClassName string) (string, error) {
	storageClassName, err := c.getStorageClassNameFromClaimName(ctx, namespace, claimName)
	if err != nil {
		klog.V(2).Infof("Error getting storage class name for claim %s in namespace %s: %v", claimName, namespace, err)
		return "", fmt.Errorf("unable to determine volume snapshot class name for infra source volume")
	}
	if storageClassName == "" && !c.storageClassEnforcement.AllowDefault {
		return "", fmt.Errorf("unable to determine volume snapshot class name for snapshot creation, and default not allowed")
	} else if storageClassName != "" && !(util.Contains(c.storageClassEnforcement.AllowList, storageClassName) || c.storageClassEnforcement.AllowAll) {
		return "", fmt.Errorf("unable to determine volume snapshot class name for snapshot creation, no valid snapshot classes found")
	}
	snapshotClassNames := c.getInfraSnapshotClassesFromInfraStorageClassName(storageClassName)
	if util.Contains(snapshotClassNames, snapshotClassName) {
		return snapshotClassName, nil
	}
	if !(c.storageClassEnforcement.AllowAll || c.storageClassEnforcement.AllowDefault) {
		tenantSnapshotClasses := c.getTenantSnapshotClassesFromInfraStorageClassName(storageClassName)
		if len(tenantSnapshotClasses) > 0 {
			if snapshotClassName == "" {
				return "", fmt.Errorf("unable to determine volume snapshot class name for snapshot creation, valid snapshot classes are %v", tenantSnapshotClasses)
			} else {
				return "", fmt.Errorf("volume snapshot class %s is not compatible with PVC with storage class %s, valid snapshot classes for this pvc are %v", snapshotClassName, storageClassName, tenantSnapshotClasses)
			}
		} else {
			return "", fmt.Errorf("unable to determine volume snapshot class name for snapshot creation, no valid snapshot classes found")
		}
	}
	return "", nil
}

func (c *client) getInfraSnapshotClassesFromInfraStorageClassName(storageClassName string) []string {
	for _, storageSnapshotMapping := range c.infraTenantStorageSnapshotMapping {
		for _, storageClass := range storageSnapshotMapping.StorageClasses {
			if storageClassName == storageClass {
				infraSnapshotClasses := []string{}
				for _, snapshotClasses := range storageSnapshotMapping.VolumeSnapshotClasses {
					infraSnapshotClasses = append(infraSnapshotClasses, snapshotClasses.Infra)
				}
				return infraSnapshotClasses
			}
		}
	}
	return nil
}

func (c *client) getTenantSnapshotClassesFromInfraStorageClassName(storageClassName string) []string {
	for _, storageSnapshotMapping := range c.infraTenantStorageSnapshotMapping {
		for _, storageClass := range storageSnapshotMapping.StorageClasses {
			if storageClassName == storageClass {
				tenantSnapshotClasses := []string{}
				for _, snapshotClasses := range storageSnapshotMapping.VolumeSnapshotClasses {
					tenantSnapshotClasses = append(tenantSnapshotClasses, snapshotClasses.Tenant)
				}
				return tenantSnapshotClasses
			}
		}
	}
	return nil
}

// Determine the name of the volume associated with the passed in claim name
func (c *client) getStorageClassNameFromClaimName(ctx context.Context, namespace, claimName string) (string, error) {
	volumeClaim, err := c.infraKubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, claimName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Error getting volume claim %s in namespace %s: %v", claimName, namespace, err)
		return "", err
	}
	klog.V(5).Infof("found volumeClaim %#v", volumeClaim)
	storageClassName := ""
	if volumeClaim.Spec.StorageClassName != nil {
		storageClassName = *volumeClaim.Spec.StorageClassName
	}
	return storageClassName, nil
}

func (c *client) GetVolumeSnapshot(ctx context.Context, namespace, name string) (*snapshotv1.VolumeSnapshot, error) {
	s, err := c.infraSnapClient.SnapshotV1().VolumeSnapshots(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if s != nil {
		if !containsLabels(s.Labels, c.infraLabelMap) {
			return nil, ErrInvalidSnapshot
		}
	}
	return s, nil
}

func (c *client) DeleteVolumeSnapshot(ctx context.Context, namespace, name string) error {
	s, err := c.GetVolumeSnapshot(ctx, namespace, name)
	if errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return c.infraSnapClient.SnapshotV1().VolumeSnapshots(s.GetNamespace()).Delete(ctx, s.GetName(), metav1.DeleteOptions{})
}

func (c *client) ListVolumeSnapshots(ctx context.Context, namespace string) (*snapshotv1.VolumeSnapshotList, error) {
	sl, err := labels.ValidatedSelectorFromSet(c.infraLabelMap)
	if err != nil {
		return nil, err
	}
	return c.infraSnapClient.SnapshotV1().VolumeSnapshots(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: sl.String(),
	})
}

func (c *client) buildStorageClassSnapshotClassMapping(k8sClient kubernetes.Interface, snapshotClient snapcli.Interface, infraStorageSnapMapping []util.StorageSnapshotMapping) ([]InfraTenantStorageSnapshotMapping, error) {
	provisionerMapping := make([]InfraTenantStorageSnapshotMapping, len(infraStorageSnapMapping))

	volumeSnapshotClassList, err := snapshotClient.SnapshotV1().VolumeSnapshotClasses().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for i, storageSnapshotMapping := range infraStorageSnapMapping {
		mapping := &InfraTenantStorageSnapshotMapping{
			StorageClasses: storageSnapshotMapping.StorageClasses,
		}
		mapping = appendVolumeSnapshotInfraTenantMapping(mapping, storageSnapshotMapping.VolumeSnapshotClasses, volumeSnapshotClassList.Items)
		provisionerMapping[i] = *mapping
	}

	return provisionerMapping, nil
}

func appendVolumeSnapshotInfraTenantMapping(mapping *InfraTenantStorageSnapshotMapping, infraVolumeSnapshotClasses []string, tenantVolumeSnapshotClasses []snapshotv1.VolumeSnapshotClass) *InfraTenantStorageSnapshotMapping {
	for _, infraVolumeSnapshotClass := range infraVolumeSnapshotClasses {
		tenantVolumeSnapshotClassName := ""
		for _, tenantVolumeSnapshotClass := range tenantVolumeSnapshotClasses {
			if infraVolumeSnapshotClassName, ok := tenantVolumeSnapshotClass.Parameters[InfraSnapshotClassNameParameter]; !ok {
				klog.V(4).Infof("volume snapshot class %s does not have infraSnapshotClassName parameter", tenantVolumeSnapshotClass.Name)
				continue
			} else {
				if infraVolumeSnapshotClassName == infraVolumeSnapshotClass {
					tenantVolumeSnapshotClassName = tenantVolumeSnapshotClass.Name
					break
				}
			}
		}
		mapping.VolumeSnapshotClasses = append(mapping.VolumeSnapshotClasses, InfraToTenantMapping{
			Infra:  infraVolumeSnapshotClass,
			Tenant: tenantVolumeSnapshotClassName,
		})
	}
	return mapping
}

var ErrInvalidSnapshot = goerrors.New("invalid snapshot name")
var ErrInvalidVolume = goerrors.New("invalid volume name")
