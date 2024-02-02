package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	kubevirtv1 "kubevirt.io/api/core/v1"
	v1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	cdicli "kubevirt.io/csi-driver/pkg/generated/containerized-data-importer/client-go/clientset/versioned"
	kubecli "kubevirt.io/csi-driver/pkg/generated/kubevirt/client-go/clientset/versioned"
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
}

type client struct {
	kubernetesClient *kubernetes.Clientset
	virtClient       *kubecli.Clientset
	cdiClient        *cdicli.Clientset
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
	result.virtClient = kubevirtClient
	result.cdiClient = cdiClient
	result.restClient = restClient
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
	return wait.PollImmediate(time.Second, timeout, func() (done bool, err error) {
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
	return wait.PollImmediate(time.Second, timeout, func() (done bool, err error) {
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

func PortForward(name, namespace, resource string, config *rest.Config, port int, protocol string) (StreamInterface, error) {
	return asyncSubresourceHelper(config, resource, namespace, name, buildPortForwardResourcePath(port, protocol), url.Values{})
}

type StreamInterface interface {
	Stream(options StreamOptions) error
	AsConn() net.Conn
}
