package kubevirt

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	kubevirtapiv1 "kubevirt.io/client-go/api/v1"
	"kubevirt.io/client-go/kubecli"
	cdiv1alpha1 "kubevirt.io/containerized-data-importer/pkg/apis/core/v1alpha1"
)

//go:generate mockgen -source=./client.go -destination=./mock/client_generated.go -package=mock

// ClientBuilderFuncType is function type for building infra-cluster clients
type ClientBuilderFuncType func(kubeconfig string) (Client, error)

// Client is a wrapper object for actual infra-cluster clients: kubernetes and the kubevirt
type Client interface {
	Ping(ctx context.Context) error
	ListVirtualMachines(namespace string) ([]kubevirtapiv1.VirtualMachineInstance, error)
	DeleteDataVolume(namespace string, name string) error
	CreateDataVolume(namespace string, dataVolume *cdiv1alpha1.DataVolume) (*cdiv1alpha1.DataVolume, error)
	AddVolumeToVM(namespace string, vmName string, hotPlugRequest *kubevirtapiv1.HotplugVolumeRequest) error
	RemoveVolumeFromVM(namespace string, vmName string, hotPlugRequest *kubevirtapiv1.HotplugVolumeRequest) error
}

type client struct {
	kubernetesClient *kubernetes.Clientset
	virtClient       kubecli.KubevirtClient
}

// New creates our client wrapper object for the actual kubeVirt and kubernetes clients we use.
func NewClient(config *rest.Config) (Client, error) {
	result := &client{}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	result.kubernetesClient = clientset

	kubevirtClient, err := kubecli.GetKubevirtClientFromRESTConfig(config)
	if err != nil {
		return nil, err
	}
	result.virtClient = kubevirtClient
	return result, nil
}

func (c *client) AddVolumeToVM(namespace string, vmName string, hotPlugRequest *kubevirtapiv1.HotplugVolumeRequest) error {
	return c.virtClient.VirtualMachine(namespace).AddVolume(vmName, hotPlugRequest)
}

func (c *client) RemoveVolumeFromVM(namespace string, vmName string, hotPlugRequest *kubevirtapiv1.HotplugVolumeRequest) error {
	return c.virtClient.VirtualMachine(namespace).RemoveVolume(vmName, hotPlugRequest)
}

func (c *client) ListVirtualMachines(namespace string) ([]kubevirtapiv1.VirtualMachineInstance, error) {
	list, err := c.virtClient.VirtualMachineInstance(namespace).List(&metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (c *client) CreateDataVolume(namespace string, dataVolume *cdiv1alpha1.DataVolume) (*cdiv1alpha1.DataVolume, error) {
	return c.virtClient.CdiClient().CdiV1alpha1().DataVolumes(namespace).Create(dataVolume)
}

func (c *client) Ping(ctx context.Context) error {
	_, err := c.kubernetesClient.ServerVersion()
	return err
}

func (c *client) DeleteDataVolume(namespace string, name string) error {
	return c.virtClient.CdiClient().CdiV1alpha1().DataVolumes(namespace).Delete(name, &metav1.DeleteOptions{})
}
