package kubevirt

import (
	"context"
	//"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	kubevirtapiv1 "kubevirt.io/client-go/api/v1"
	"kubevirt.io/client-go/kubecli"
	csiv1alpha1 "kubevirt.io/containerized-data-importer/pkg/apis/core/v1alpha1"
)

//go:generate mockgen -source=./client.go -destination=./mock/client_generated.go -package=mock

// ClientBuilderFuncType is function type for building infra-cluster clients
type ClientBuilderFuncType func(kubeconfig string) (Client, error)

// Client is a wrapper object for actual infra-cluster clients: kubernetes and the kubevirt
type Client interface {
	Ping(ctx context.Context) error
	GetNamespace(ctx context.Context, name string) (*corev1.Namespace, error)
	ListNamespace(ctx context.Context) (*corev1.NamespaceList, error)
	GetStorageClass(ctx context.Context, name string) (*storagev1.StorageClass, error)
	ListVirtualMachines(namespace string) ([]kubevirtapiv1.VirtualMachineInstance, error)
	DeleteDataVolume(namespace string, name string) error
	CreateDataVolume(namespace string, dataVolume csiv1alpha1.DataVolume) error
	GetDataVolume(namespace string, name string) (*csiv1alpha1.DataVolume, error)
	ListDataVolumes(namespace string) ([]csiv1alpha1.DataVolume, error)
	GetVMI(ctx context.Context, namespace string, name string) (*kubevirtapiv1.VirtualMachineInstance, error)
	AddVolumeToVM(namespace string, vmName string, hotPlugRequest kubevirtapiv1.HotplugVolumeRequest) error
	RemoveVolumeFromVM(namespace string, vmName string, hotPlugRequest kubevirtapiv1.HotplugVolumeRequest) error
}

type client struct {
	kubernetesClient *kubernetes.Clientset
	virtClient       kubecli.KubevirtClient
	dynamicClient    dynamic.Interface
}

func (c *client) AddVolumeToVM(namespace string, vmName string, hotPlugRequest kubevirtapiv1.HotplugVolumeRequest) error {
	return c.virtClient.VirtualMachine(namespace).AddVolume(vmName, &hotPlugRequest)
}

func (c *client) RemoveVolumeFromVM(namespace string, vmName string, hotPlugRequest kubevirtapiv1.HotplugVolumeRequest) error {
	return c.virtClient.VirtualMachine(namespace).RemoveVolume(vmName, &hotPlugRequest)
}

func (c *client) ListVirtualMachines(namespace string) ([]kubevirtapiv1.VirtualMachineInstance, error) {
	list, err := c.virtClient.VirtualMachineInstance(namespace).List(&metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *client) CreateDataVolume(namespace string, dataVolume csiv1alpha1.DataVolume) error {
	_, err := c.virtClient.CdiClient().CdiV1alpha1().DataVolumes(namespace).Create(&dataVolume)
	return err
}

// New creates our client wrapper object for the actual kubeVirt and kubernetes clients we use.
func NewClient(config *rest.Config) (Client, error) {
	result := &client{}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	result.kubernetesClient = clientset
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	result.dynamicClient = dynamicClient

	kubevirtClient, err := kubecli.GetKubevirtClientFromRESTConfig(config)
	if err != nil {
		return nil, err
	}
	result.virtClient = kubevirtClient
	return result, nil
}

func (c *client) Ping(ctx context.Context) error {
	_, err := c.kubernetesClient.ServerVersion()
	return err
}
func (c *client) GetNamespace(ctx context.Context, name string) (*corev1.Namespace, error) {
	return c.kubernetesClient.CoreV1().Namespaces().Get(name, metav1.GetOptions{})
}

func (c *client) ListNamespace(ctx context.Context) (*corev1.NamespaceList, error) {
	return c.kubernetesClient.CoreV1().Namespaces().List(metav1.ListOptions{})
}

func (c *client) GetStorageClass(ctx context.Context, name string) (*storagev1.StorageClass, error) {
	return c.kubernetesClient.StorageV1().StorageClasses().Get(name, metav1.GetOptions{})
}

// The functions bellow are used for the destroy command
// Use Dynamic cluster for those actions (list and delete)

func (c *client) ListVirtualMachineInstancesNames(namespace string, requiredLabels map[string]string) ([]string, error) {
	opts := &metav1.ListOptions{
		LabelSelector: labels.FormatLabels(requiredLabels),
	}
	instanceList, err := c.virtClient.VirtualMachineInstance(namespace).List(opts)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, vmi := range instanceList.Items {
		names = append(names, vmi.Name)
	}
	return names, nil
}

func (c *client) DeleteDataVolume(namespace string, name string) error {
	return c.virtClient.CdiClient().CdiV1alpha1().DataVolumes(namespace).Delete(name, &metav1.DeleteOptions{})
}

func (c *client) ListDataVolumes(namespace string) ([]csiv1alpha1.DataVolume, error) {
	list, err := c.virtClient.CdiClient().CdiV1alpha1().
		DataVolumes(namespace).
		List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *client) GetDataVolume(namespace string, name string) (*csiv1alpha1.DataVolume, error) {
	get, err := c.virtClient.CdiClient().CdiV1alpha1().DataVolumes(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return get, nil
}

func (c *client) GetVMI(_ context.Context, namespace string, name string) (*kubevirtapiv1.VirtualMachineInstance, error) {
	vm, err := c.virtClient.VirtualMachineInstance(namespace).Get(name, &metav1.GetOptions{})
	return vm, err
}
