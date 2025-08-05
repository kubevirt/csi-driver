package kubevirt

import (
	"context"
	"encoding/json"
	goerrors "errors"
	"fmt"
	"strings"
	"sync"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
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
	vmSubresourceURL                = "/apis/subresources.kubevirt.io/%s/namespaces/%s/virtualmachines/%s/%s"
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
	GetWorkloadManagingVirtualMachine(ctx context.Context, namespace, name string) (*kubevirtv1.VirtualMachine, error)
	DeleteDataVolume(ctx context.Context, namespace string, name string) error
	CreateDataVolume(ctx context.Context, namespace string, dataVolume *cdiv1.DataVolume) (*cdiv1.DataVolume, error)
	GetDataVolume(ctx context.Context, namespace string, name string) (*cdiv1.DataVolume, error)
	GetPersistentVolumeClaim(ctx context.Context, namespace string, claimName string) (*k8sv1.PersistentVolumeClaim, error)
	ExpandPersistentVolumeClaim(ctx context.Context, namespace string, claimName string, size int64) error
	AddVolumeToVM(ctx context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.AddVolumeOptions) error
	RemoveVolumeFromVM(ctx context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.RemoveVolumeOptions) error
	RemoveVolumeFromVMI(ctx context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.RemoveVolumeOptions) error
	EnsureVolumeAvailable(ctx context.Context, namespace, vmName, volumeName string, timeout time.Duration) error
	EnsureVolumeAvailableVM(ctx context.Context, namespace, name, volumeName string) (bool, error)
	EnsureVolumeRemoved(ctx context.Context, namespace, vmName, volumeName string, timeout time.Duration) error
	EnsureVolumeRemovedVM(ctx context.Context, namespace, name, volumeName string) (bool, error)
	EnsureSnapshotReady(ctx context.Context, namespace, name string, timeout time.Duration) error
	EnsureControllerResize(ctx context.Context, namespace, claimName string, timeout time.Duration) error
	CreateVolumeSnapshot(ctx context.Context, namespace, name, claimName, snapshotClassName string) (*snapshotv1.VolumeSnapshot, error)
	GetVolumeSnapshot(ctx context.Context, namespace, name string) (*snapshotv1.VolumeSnapshot, error)
	DeleteVolumeSnapshot(ctx context.Context, namespace, name string) error
	ListVolumeSnapshots(ctx context.Context, namespace string) (*snapshotv1.VolumeSnapshotList, error)
}

type client struct {
	infraKubernetesClient                      kubernetes.Interface
	tenantKubernetesClient                     kubernetes.Interface
	virtClient                                 kubecli.Interface
	cdiClient                                  cdicli.Interface
	infraSnapClient                            snapcli.Interface
	tenantSnapClient                           snapcli.Interface
	restClient                                 *rest.RESTClient
	storageClassEnforcement                    util.StorageClassEnforcement
	infraLabelMap                              map[string]string
	volumePrefix                               string
	infraTenantStorageSnapshotMapping          []InfraTenantStorageSnapshotMapping
	infraTenantStorageSnapshotMappingPopulated bool
	mu                                         sync.Mutex
}

// NewClient New creates our client wrapper object for the actual kubeVirt and kubernetes clients we use.
func NewClient(infraConfig *rest.Config, infraClusterLabelMap map[string]string, tenantKubernetesClient kubernetes.Interface, tenantSnapshotClient snapcli.Interface, storageClassEnforcement util.StorageClassEnforcement, prefix string) (Client, error) {
	result := &client{}

	Scheme := runtime.NewScheme()
	// Could reduce this to just the metav1.Status{} type for error decoding
	// But someone else will likely trip on another type in the future
	if err := k8sv1.AddToScheme(Scheme); err != nil {
		return nil, err
	}
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
	result.tenantSnapClient = tenantSnapshotClient
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

func (c *client) getStorageSnapshotMapping() ([]InfraTenantStorageSnapshotMapping, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.infraTenantStorageSnapshotMappingPopulated {
		storageSnapshotMapping, err := c.buildStorageClassSnapshotClassMapping(c.tenantSnapClient, c.storageClassEnforcement.StorageSnapshotMapping)
		if err != nil {
			return nil, err
		}
		c.infraTenantStorageSnapshotMapping = storageSnapshotMapping
		c.infraTenantStorageSnapshotMappingPopulated = true
		klog.V(5).Infof("Populated Storage class snapshot class mapping: %#v", c.infraTenantStorageSnapshotMapping)
	}
	return c.infraTenantStorageSnapshotMapping, nil
}

// AddVolumeToVM performs a hotplug of a DataVolume to a VM
func (c *client) AddVolumeToVM(ctx context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.AddVolumeOptions) error {
	uri := fmt.Sprintf(vmSubresourceURL, kubevirtv1.ApiStorageVersion, namespace, vmName, "addvolume")

	JSON, err := json.Marshal(hotPlugRequest)

	if err != nil {
		return err
	}

	return c.restClient.Put().AbsPath(uri).Body([]byte(JSON)).Do(ctx).Error()
}

// RemoveVolumeFromVM perform hotunplug of a DataVolume from a VM
func (c *client) RemoveVolumeFromVM(ctx context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.RemoveVolumeOptions) error {
	uri := fmt.Sprintf(vmSubresourceURL, kubevirtv1.ApiStorageVersion, namespace, vmName, "removevolume")

	JSON, err := json.Marshal(hotPlugRequest)

	if err != nil {
		return err
	}

	return c.restClient.Put().AbsPath(uri).Body([]byte(JSON)).Do(ctx).Error()
}

// RemoveVolumeFromVMI perform hotunplug of a DataVolume from a VMI
func (c *client) RemoveVolumeFromVMI(ctx context.Context, namespace string, vmName string, hotPlugRequest *kubevirtv1.RemoveVolumeOptions) error {
	vmiSubresourceURL := "/apis/subresources.kubevirt.io/%s/namespaces/%s/virtualmachineinstances/%s/%s"
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
				return c.EnsureVolumeAvailableVM(ctx, namespace, vmName, volumeName)
			}
		}

		return false, nil
	})
}

// EnsureVolumeRemoved checks to make sure the volume is removed from the node before returning, checks for 2 minutes
func (c *client) EnsureVolumeRemoved(ctx context.Context, namespace, vmName, volumeName string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (done bool, err error) {
		vmi, err := c.GetVirtualMachine(ctx, namespace, vmName)
		if err != nil {
			if !errors.IsNotFound(err) {
				return false, err
			}
			// No VMI, volume considered removed if it's not on the VM
			return c.EnsureVolumeRemovedVM(ctx, namespace, vmName, volumeName)
		}
		for _, volume := range vmi.Status.VolumeStatus {
			if volume.Name == volumeName {
				return false, nil
			}
		}

		return c.EnsureVolumeRemovedVM(ctx, namespace, vmName, volumeName)
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

// EnsureControllerResize checks that a ControllerExpandVolume is finished on the infra storage, checks for 2 minutes
func (c *client) EnsureControllerResize(ctx context.Context, namespace, claimName string, timeout time.Duration) error {
	pvc, err := c.GetPersistentVolumeClaim(ctx, namespace, claimName)
	if err != nil {
		return err
	}
	pvName := pvc.Spec.VolumeName
	return wait.PollUntilContextTimeout(ctx, time.Second, timeout, true, func(ctx context.Context) (done bool, err error) {
		pvcSize := pvc.Spec.Resources.Requests[k8sv1.ResourceStorage]
		pv, err := c.infraKubernetesClient.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("error fetching pv %q for resizing %v", pvName, err)
		}
		pvSize := pv.Spec.Capacity[k8sv1.ResourceStorage]
		// If pv size is greater or equal to requested size that means controller resize is finished
		// https://github.com/kubernetes/kubernetes/blob/6a17858ff9be5601149ded54eb33280adc2783b3/test/e2e/storage/testsuites/volume_expand.go#L419
		if pvSize.Cmp(pvcSize) >= 0 {
			return true, nil
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

// GetWorkloadManagingVirtualMachine gets a VM from the passed in namespace
func (c *client) GetWorkloadManagingVirtualMachine(ctx context.Context, namespace, name string) (*kubevirtv1.VirtualMachine, error) {
	return c.virtClient.KubevirtV1().VirtualMachines(namespace).Get(ctx, name, metav1.GetOptions{})
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

func (c *client) GetPersistentVolumeClaim(ctx context.Context, namespace string, claimName string) (*k8sv1.PersistentVolumeClaim, error) {
	pvc, err := c.infraKubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, claimName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Error getting volume claim %s in namespace %s: %v", claimName, namespace, err)
		return nil, err
	}
	if pvc != nil {
		if !containsLabels(pvc.Labels, c.infraLabelMap) || !strings.HasPrefix(pvc.GetName(), c.volumePrefix) {
			return nil, ErrInvalidVolume
		}
	}
	return pvc, nil
}

func (c *client) ExpandPersistentVolumeClaim(ctx context.Context, namespace string, claimName string, desiredSize int64) error {
	currentPVC, err := c.GetPersistentVolumeClaim(ctx, namespace, claimName)
	if err != nil {
		return err
	}
	desiredQuantity := *resource.NewQuantity(desiredSize, resource.DecimalSI)
	currentQuantity := currentPVC.Spec.Resources.Requests.Storage()
	if currentQuantity.Cmp(desiredQuantity) >= 0 {
		klog.V(5).Infof("Volume %s of quantity %v is larger than requested quantity %v, no need to expand", claimName, *currentQuantity, desiredQuantity)
		return nil
	}

	patchData := fmt.Sprintf(`[{"op":"add","path":"/spec/resources/requests/storage","value":"%d" }]`, desiredSize)
	_, err = c.infraKubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Patch(ctx, claimName, types.JSONPatchType, []byte(patchData), metav1.PatchOptions{})
	if err != nil {
		return err
	}

	return nil
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
	if c.storageClassEnforcement.AllowDefault {
		// Allow default is set to true, return blank snapshot class name so it remains blank in the created volume snapshot in the infra cluster.
		return "", nil
	}
	if storageClassName == "" {
		return "", fmt.Errorf("unable to determine volume snapshot class name for snapshot creation, and default not allowed")
	} else if storageClassName != "" && !(util.Contains(c.storageClassEnforcement.AllowList, storageClassName) || c.storageClassEnforcement.AllowAll) {
		return "", fmt.Errorf("unable to determine volume snapshot class name for snapshot creation, no valid snapshot classes found based on storage class [%s]", storageClassName)
	}
	snapshotClassNames, err := c.getInfraSnapshotClassesFromInfraStorageClassName(storageClassName)
	if err != nil {
		return "", err
	}
	if util.Contains(snapshotClassNames, snapshotClassName) {
		return snapshotClassName, nil
	}
	if !(c.storageClassEnforcement.AllowAll || c.storageClassEnforcement.AllowDefault) {
		tenantSnapshotClasses, err := c.getTenantSnapshotClassesFromInfraStorageClassName(storageClassName)
		if err != nil {
			return "", err
		}
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

func (c *client) getInfraSnapshotClassesFromInfraStorageClassName(storageClassName string) ([]string, error) {
	infraTenantStorageSnapshotMapping, err := c.getStorageSnapshotMapping()
	if err != nil {
		return nil, err
	}
	for _, storageSnapshotMapping := range infraTenantStorageSnapshotMapping {
		for _, storageClass := range storageSnapshotMapping.StorageClasses {
			if storageClassName == storageClass {
				infraSnapshotClasses := []string{}
				for _, snapshotClasses := range storageSnapshotMapping.VolumeSnapshotClasses {
					infraSnapshotClasses = append(infraSnapshotClasses, snapshotClasses.Infra)
				}
				return infraSnapshotClasses, nil
			}
		}
	}
	return nil, nil
}

func (c *client) getTenantSnapshotClassesFromInfraStorageClassName(storageClassName string) ([]string, error) {
	infraTenantStorageSnapshotMapping, err := c.getStorageSnapshotMapping()
	if err != nil {
		return nil, err
	}
	for _, storageSnapshotMapping := range infraTenantStorageSnapshotMapping {
		for _, storageClass := range storageSnapshotMapping.StorageClasses {
			if storageClassName == storageClass {
				tenantSnapshotClasses := []string{}
				for _, snapshotClasses := range storageSnapshotMapping.VolumeSnapshotClasses {
					tenantSnapshotClasses = append(tenantSnapshotClasses, snapshotClasses.Tenant)
				}
				return tenantSnapshotClasses, nil
			}
		}
	}
	return nil, nil
}

// Determine the name of the volume associated with the passed in claim name
func (c *client) getStorageClassNameFromClaimName(ctx context.Context, namespace, claimName string) (string, error) {
	volumeClaim, err := c.infraKubernetesClient.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, claimName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Error getting volume claim %s in namespace %s: %v", claimName, namespace, err)
		return "", err
	}
	storageClassName := ""
	if volumeClaim.Spec.StorageClassName != nil {
		storageClassName = *volumeClaim.Spec.StorageClassName
	}
	klog.V(5).Infof("found storageClassName %s for volume %s", storageClassName, claimName)
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

func (c *client) buildStorageClassSnapshotClassMapping(snapshotClient snapcli.Interface, infraStorageSnapMapping []util.StorageSnapshotMapping) ([]InfraTenantStorageSnapshotMapping, error) {
	klog.V(5).Infof("Building storage class snapshot class mapping, %#v", infraStorageSnapMapping)
	provisionerMapping := make([]InfraTenantStorageSnapshotMapping, len(infraStorageSnapMapping))

	volumeSnapshotClassList, err := snapshotClient.SnapshotV1().VolumeSnapshotClasses().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	klog.V(5).Infof("Volume snapshot class list: %#v", volumeSnapshotClassList.Items)
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

func (c *client) EnsureVolumeRemovedVM(ctx context.Context, namespace, name, volumeName string) (bool, error) {
	vm, err := c.virtClient.KubevirtV1().VirtualMachines(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return false, err
		}
		// No VM, vacuously removed
		return true, nil
	}

	for _, volume := range vm.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil && volume.DataVolume == nil {
			continue
		}
		if volume.Name == volumeName {
			return false, nil
		}
	}

	return true, nil
}

func (c *client) EnsureVolumeAvailableVM(ctx context.Context, namespace, name, volumeName string) (bool, error) {
	vm, err := c.virtClient.KubevirtV1().VirtualMachines(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if !errors.IsNotFound(err) {
			return false, err
		}
		// No VM, something's not right, can't assume availability
		return false, nil
	}

	for _, volume := range vm.Spec.Template.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil && volume.DataVolume == nil {
			continue
		}
		if volume.Name == volumeName {
			return true, nil
		}
	}

	return false, nil
}

var ErrInvalidSnapshot = goerrors.New("invalid snapshot name")
var ErrInvalidVolume = goerrors.New("invalid volume name")
