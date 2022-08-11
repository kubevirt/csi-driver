package functional

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
	ocproutev1 "github.com/openshift/api/route/v1"
	routesclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	v1network "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/yaml"
	apiwatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd/api"
	clientwatch "k8s.io/client-go/tools/watch"
	"k8s.io/kubernetes/pkg/apis/storage"
	"kubevirt.io/client-go/kubecli"
	"kubevirt.io/csi-driver/pkg/generated"

	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/tools/clientcmd"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubevirtv1 "kubevirt.io/api/core/v1"
	testutil "kubevirt.io/kubevirt/tests/util"

	. "github.com/onsi/gomega"
)

const (
	k8sMachineName   = "k8s-machine"
	routeName        = "k8s-api"
	tenantConfigName = "tenant-config"
)

var testNamespace = "kubevirt-csi-driver-func-test-" + uuid.New().String()[0:7]
var teardownNamespace = true

func init() {
	if v, ok := os.LookupEnv("KUBEVIRT_CSI_DRIVER_FUNC_TEST_NAMESPACE"); ok {
		testNamespace = v
		teardownNamespace = false
	}

}

type InfraCluster struct {
	// kubevirt's client for creating needed resources to install the cluster on
	virtCli    kubecli.KubevirtClient
	kubeconfig clientcmd.ClientConfig
	// the infra namespace for creating resources
	namespace string
}

type TenantCluster struct {
	namespace  string
	config     *api.Config
	client     *kubernetes.Clientset
	restConfig *rest.Config
}

func PrepareEnv(clusterSetup InfraCluster) error {
	return clusterSetup.setupTenantCluster()
}

func TearDownEnv(infraCluster InfraCluster) {
	if teardownNamespace {
		err := infraCluster.virtCli.CoreV1().
			Namespaces().Delete(context.Background(), infraCluster.namespace, metav1.DeleteOptions{})
		Expect(err).ToNot(HaveOccurred(), "Failed deleting the namespace post suite. Please clean manually")
	}
}

func (c *InfraCluster) setupTenantCluster() error {
	fmt.Fprint(ginkgo.GinkgoWriter, "Preparing infrastructure for the tenant cluster\n")
	c.createNamespace()
	err := c.exposeTenantAPI()
	if err != nil {
		return err
	}

	err = c.createServiceAccount()
	if err != nil {
		return err
	}

	c.createVm()

	return nil
}

func (c *InfraCluster) exposeTenantAPI() error {
	if IsOpenShift() {
		Expect(c.createAPIRoute()).To(Succeed())
	} else {
		Expect(c.createAPIIngress()).To(Succeed())
	}
	_, err := c.virtCli.CoreV1().Services(c.namespace).Create(context.Background(), &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api",
		},
		Spec: corev1.ServiceSpec{
			Ports:           []corev1.ServicePort{{Port: 6443, TargetPort: intstr.IntOrString{IntVal: 6443}, Protocol: corev1.ProtocolTCP}},
			Selector:        map[string]string{"kubevirt.io/domain": k8sMachineName},
			Type:            "NodePort",
			SessionAffinity: "None",
		},
	}, metav1.CreateOptions{})
	return err
}

func (c *InfraCluster) createServiceAccount() error {
	fmt.Fprint(ginkgo.GinkgoWriter, "Creating service account...\n")
	reader := bytes.NewReader(generated.MustAsset("deploy/infra-cluster-service-account.yaml"))
	sa := corev1.ServiceAccount{}
	err := yaml.NewYAMLToJSONDecoder(reader).Decode(&sa)
	//printErr(err)
	if err != nil {
		return nil
	}
	_, err = c.virtCli.CoreV1().ServiceAccounts(c.namespace).Create(context.Background(), &sa, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (c *InfraCluster) createAPIRoute() error {
	fmt.Fprint(ginkgo.GinkgoWriter, "Creating API endpoint route...\n")
	r := ocproutev1.Route{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Route",
			APIVersion: ocproutev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: routeName,
		},
		Spec: ocproutev1.RouteSpec{
			TLS: &ocproutev1.TLSConfig{
				Termination: ocproutev1.TLSTerminationPassthrough},
			To: ocproutev1.RouteTargetReference{
				Kind: "Service",
				Name: "api",
			},
		},
	}

	routes, err := routesclientset.NewForConfig(c.virtCli.Config())
	if err != nil {
		return err
	}

	_, err = routes.Routes(c.namespace).Create(context.Background(), &r, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func (c *InfraCluster) createAPIIngress() error {
	// deploy kube ingress controller first
	fmt.Fprint(ginkgo.GinkgoWriter, "Deploy Kubernetes Ingress controller...\n")
	b, err := ioutil.ReadFile("tests/functional/testdata/kube-ingress-controller.yaml")
	if err != nil {
		return err
	}
	_, err = c.createObject(
		*c.virtCli.Config(),
		c.virtCli.Discovery(),
		c.namespace,
		bytes.NewReader(b))
	if !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}

	fmt.Fprint(ginkgo.GinkgoWriter, "Creating API endpoint Ingress...\n")
	var pathTypePrefix = v1network.PathTypePrefix
	i := v1network.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: routeName,
			Annotations: map[string]string{
				"kubernetes.io/ingress.allow-http":             "false",
				"nginx.ingress.kubernetes.io/ssl-passthrough":  "true",
				"nginx.ingress.kubernetes.io/backend-protocol": "HTTPS",
			},
		},
		Spec: v1network.IngressSpec{
			Rules: []v1network.IngressRule{
				{
					Host: "",
					IngressRuleValue: v1network.IngressRuleValue{
						HTTP: &v1network.HTTPIngressRuleValue{
							Paths: []v1network.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathTypePrefix,
									Backend: v1network.IngressBackend{
										Service: &v1network.IngressServiceBackend{
											Name: "api",
											Port: v1network.ServiceBackendPort{
												Number: 443,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	ingress, err := c.virtCli.NetworkingV1().Ingresses(c.namespace).Create(context.Background(), &i, metav1.CreateOptions{})
	fmt.Fprintf(ginkgo.GinkgoWriter, "created the ingress %v %v\n", ingress, err)
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	timeout, cancelFunc := context.WithTimeout(context.Background(), time.Minute*10)
	defer cancelFunc()
	_, _ = clientwatch.UntilWithSync(
		timeout,
		cache.NewListWatchFromClient(c.virtCli.NetworkingV1().RESTClient(), "ingresses",
			c.namespace, fields.OneTermEqualSelector("metadata.name", routeName)),
		&v1network.Ingress{},
		nil,
		func(event apiwatch.Event) (bool, error) {
			switch event.Type {
			case apiwatch.Added, apiwatch.Modified:
			default:
				return false, nil
			}
			i, ok := event.Object.(*v1network.Ingress)
			if !ok {
				ginkgo.Fail("couldn't find the ingress object")
			}

			if len(i.Status.LoadBalancer.Ingress) == 0 {
				return false, nil
			}
			return true, nil
		},
	)
	return nil
}

func (c *InfraCluster) createNamespace() {
	fmt.Fprint(ginkgo.GinkgoWriter, "Creating the test namespace...\n")
	_, err := c.virtCli.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: c.namespace,
		},
	}, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

func (c *InfraCluster) createVm() {
	routes, err := routesclientset.NewForConfig(c.virtCli.Config())
	Expect(err).NotTo(HaveOccurred())
	var ingressOrRouteHostname string
	if IsOpenShift() {
		route, err := routes.Routes(c.namespace).Get(context.Background(), routeName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		ingressOrRouteHostname = route.Spec.Host
	} else {
		ingress, err := c.virtCli.NetworkingV1().Ingresses(c.namespace).Get(context.Background(), routeName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		fmt.Fprintf(ginkgo.GinkgoWriter, "fetching the ingress route hostname %s\n", ingress.Spec.Rules)
		ingressOrRouteHostname = ingress.Status.LoadBalancer.Ingress[0].IP
	}

	fmt.Fprint(ginkgo.GinkgoWriter, "Creating a VM for k8s deployment...\n")

	k8sMachine, rootDv, secret := newK8sMachine(c.kubeconfig, ingressOrRouteHostname, c.namespace)

	_, err = c.virtCli.CdiClient().CdiV1beta1().DataVolumes(c.namespace).Create(context.Background(), rootDv, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}

	_, err = c.virtCli.CoreV1().Secrets(c.namespace).Create(context.Background(), secret, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
	_, err = c.virtCli.VirtualMachine(c.namespace).Create(k8sMachine)
	if !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
	Eventually(func() bool {
		vmi, _ := c.virtCli.VirtualMachineInstance(c.namespace).Get(k8sMachineName, &metav1.GetOptions{})
		return vmi.Status.Phase == kubevirtv1.Running
	}, 40*time.Minute, 30*time.Second).Should(BeTrue(), "failed to get the vmi Running")

}

func (c *InfraCluster) createObject(restConfig rest.Config, discovery discovery.DiscoveryInterface, namespace string, reader *bytes.Reader) (runtime.Object, error) {
	objs, err := objectsFromYAML(reader)
	if err != nil {
		return nil, err
	}
	var createdObjs []runtime.Object
	for _, obj := range objs {
		gvk := obj.GetObjectKind().GroupVersionKind()
		restClient, _ := newRestClient(restConfig, gvk.GroupVersion())
		groupResources, err := restmapper.GetAPIGroupResources(discovery)
		if err != nil {
			return nil, err
		}
		rm := restmapper.NewDiscoveryRESTMapper(groupResources)
		mapping, err := rm.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return nil, err
		}
		restHelper := resource.NewHelper(restClient, mapping)

		if ns, _ := meta.NewAccessor().Namespace(obj); ns != "" {
			namespace = ns
		}
		created, err := restHelper.Create(namespace, true, obj)
		if err != nil && !errors.IsAlreadyExists(err) {
			return nil, err
		}
		createdObjs = append(createdObjs, created)
	}
	// for now support returning the first object only.
	return createdObjs[0], nil
}

func objectsFromYAML(reader *bytes.Reader) ([]runtime.Object, error) {
	// register CSI v1. CSI v1 is missing because client-go is pinned to 0.16 because of kubevirt client deps.
	newScheme := runtime.NewScheme()
	newScheme.AddKnownTypes(
		schema.GroupVersion{Group: storage.GroupName, Version: "v1"},
		&storage.CSINode{},
		&storage.CSINodeList{},
		&storage.CSIDriver{},
		&storage.CSIDriverList{},
	)
	//factory := serializer.NewCodecFactory(newScheme)
	//csiDecode := factory.UniversalDecoder(schema.GroupVersion{Group: storage.GroupName, Version: "v1"}).Decode
	decode := scheme.Codecs.UniversalDeserializer().Decode
	all, _ := ioutil.ReadAll(reader)
	d := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(all), 4096)
	// data from reader may contain multi objects, stream and get a list
	var o []runtime.Object
	for {
		ext := runtime.RawExtension{}
		if err := d.Decode(&ext); err != nil {
			if err == io.EOF {
				break
			}
		}
		ext.Raw = bytes.TrimSpace(ext.Raw)
		if len(ext.Raw) == 0 || bytes.Equal(ext.Raw, []byte("null")) {
			continue
		}
		obj, _, err := decode(ext.Raw, nil, nil)
		if err != nil {
			return nil, err
		}
		o = append(o, obj)
	}
	return o, nil
}

func newRestClient(restConfig rest.Config, gv schema.GroupVersion) (rest.Interface, error) {
	restConfig.ContentConfig = resource.UnstructuredPlusDefaultContentConfig()
	restConfig.GroupVersion = &gv
	if len(gv.Group) == 0 {
		restConfig.APIPath = "/api"
	} else {
		restConfig.APIPath = "/apis"
	}

	return rest.RESTClientFor(&restConfig)
}

var running = true

func newK8sMachine(config clientcmd.ClientConfig, ingressOrRouteHostname string, namespace string) (*kubevirtv1.VirtualMachine, *cdiv1.DataVolume, *corev1.Secret) {
	vmiTemplateSpec := &kubevirtv1.VirtualMachineInstanceTemplateSpec{}
	vm := kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name: k8sMachineName,
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Running:  &running,
			Template: vmiTemplateSpec,
		},
	}

	mem := apiresource.MustParse("16G")
	vmiTemplateSpec.ObjectMeta.Labels = map[string]string{
		"kubevirt.io/domain": k8sMachineName,
	}
	vmiTemplateSpec.Spec.Domain = kubevirtv1.DomainSpec{
		CPU: &kubevirtv1.CPU{
			Cores:   4,
			Sockets: 1,
		},
		Memory: &kubevirtv1.Memory{
			Guest: &mem,
		},
	}
	vmiTemplateSpec.Spec.Domain.Resources.Requests = corev1.ResourceList{
		corev1.ResourceMemory: mem,
	}

	dataVolume := newDataVolume(
		"root-disk-dv",
		"https://cloud.centos.org/centos/7/images/CentOS-7-x86_64-GenericCloud-2009.qcow2c",
		"15G")

	cloudInitDisk := "cloudinit-disk"
	rootDisk := "root-disk"
	vmiTemplateSpec.Spec.Domain.Devices.Disks = append(vmiTemplateSpec.Spec.Domain.Devices.Disks, kubevirtv1.Disk{
		Name: cloudInitDisk,
		DiskDevice: kubevirtv1.DiskDevice{
			Disk: &kubevirtv1.DiskTarget{
				Bus: kubevirtv1.DiskBusVirtio,
			},
		},
	})
	var first uint = 1
	vmiTemplateSpec.Spec.Domain.Devices.Disks = append(vmiTemplateSpec.Spec.Domain.Devices.Disks, kubevirtv1.Disk{
		BootOrder: &first,
		Name:      rootDisk,
		DiskDevice: kubevirtv1.DiskDevice{
			Disk: &kubevirtv1.DiskTarget{
				Bus: kubevirtv1.DiskBusVirtio,
			},
		},
	})

	rawConfig, _ := config.RawConfig()
	kubeconfig, _ := clientcmd.Write(rawConfig)
	b64Config := base64.StdEncoding.EncodeToString(kubeconfig)
	cloudinitdata = strings.Replace(cloudinitdata, "@@infra-cluster-kubeconfig@@", b64Config, 1)
	cloudinitdata = strings.Replace(cloudinitdata, "@@tenant-cluster-name@@", ingressOrRouteHostname, 1)
	cloudinitdata = strings.Replace(cloudinitdata, "@@test-namespace@@", namespace, 1)
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "userdata",
		},
		Data: map[string][]byte{"userdata": []byte(cloudinitdata)},
	}
	vmiTemplateSpec.Spec.Volumes = append(vmiTemplateSpec.Spec.Volumes, kubevirtv1.Volume{
		Name: cloudInitDisk,
		VolumeSource: kubevirtv1.VolumeSource{
			CloudInitNoCloud: &kubevirtv1.CloudInitNoCloudSource{
				UserDataSecretRef: &corev1.LocalObjectReference{Name: "userdata"},
			},
		},
	})

	vmiTemplateSpec.Spec.Volumes = append(vmiTemplateSpec.Spec.Volumes, kubevirtv1.Volume{
		Name: rootDisk,
		VolumeSource: kubevirtv1.VolumeSource{
			DataVolume: &kubevirtv1.DataVolumeSource{
				Name: dataVolume.Name,
			},
		},
	})

	return &vm, dataVolume, &secret
}

func newDataVolume(name, imageUrl, diskSize string) *cdiv1.DataVolume {
	quantity, err := apiresource.ParseQuantity(diskSize)
	testutil.PanicOnError(err)
	dataVolume := &cdiv1.DataVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: cdiv1.DataVolumeSpec{
			Source: &cdiv1.DataVolumeSource{
				HTTP: &cdiv1.DataVolumeSourceHTTP{
					URL: imageUrl,
				},
			},
			PVC: &corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						"storage": quantity,
					},
				},
			},
		},
	}

	dataVolume.TypeMeta = metav1.TypeMeta{
		APIVersion: "cdi.kubevirt.io/v1alpha1",
		Kind:       "DataVolume",
	}

	return dataVolume
}

var cloudinitdata = `#cloud-config
password: centos
chpasswd:
  expire: false

ssh_authorized_keys:
  - ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDSlI9o9rPYrlVvXTtCt02A+Gcf2674ZPBFLpkaNzjsiM4bTTNNOJ6PgnOs6dBWxT2XPOSQO6y13CeWGdZLuLXhczz8Y4KB520tkPW3fKjDiJqneWEd1sxPzdHy1Vf4icxJIXMcx7Mia/8B2XrXNsTbQnOCjRJT0dEbIbXSELLnTEYfc+L3z2+klNKos2JNchfuiTw2FLGrY5x9CwX9Nanrx6kGDPmVO68ugtsQL20mKMwuGCnEkIPGsNP0eN0Bk1vVR+k0MDaII5nUpK1glh2reL3BPVCFhKh2xzASvQqK2mr8gqzbhA/LSVA8awNnib5o55beP7vr/yECkpe0f931LD3hg1O8qEeubfxKbOcUY1rDUOnyOuetB2bNE8TiAnrtY/xne8mhhBnobHwUkUMVU3J6szm58AaoQmclVsogBIetF0bi75CnOD9fY84SLsCKbHLCGkfEXXEnjErAD27UDGMSD3EmCefP41VBcq/nR7E/ns4aqLn8GIPOMt4amPM= rgolan@rgolan.local
output: {all: '| tee -a /var/log/cloud-init-output.log'}
write_files:
  - content: |
      net.bridge.bridge-nf-call-ip6tables = 1
      net.bridge.bridge-nf-call-iptables = 1
    path: /etc/sysctl.d/k8s.conf
  - content: |
      [kubernetes]
      name=Kubernetes
      baseurl=https://packages.cloud.google.com/yum/repos/kubernetes-el7-\$basearch
      enabled=1
      gpgcheck=1
      repo_gpgcheck=1
      gpgkey=https://packages.cloud.google.com/yum/doc/yum-key.gpg https://packages.cloud.google.com/yum/doc/rpm-package-key.gpg
      exclude=kubelet kubeadm kubectl
    path: /etc/yum.repos.d/kubernetes.repo
  - content: @@infra-cluster-kubeconfig@@
    encoding: base64
    path: /root/infracluster.kubeconfig

runcmd:
  - sysctl --system
  - yum-config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
  - yum install -y docker kubeadm
  - systemctl enable --now docker
  - setenforce 0
  - sed -i 's/^SELINUX=enforcing$/SELINUX=permissive/' /etc/selinux/config
  - yum install -y kubelet kubeadm kubectl --disableexcludes=kubernetes
  - systemctl enable --now kubelet
  - kubeadm init --pod-network-cidr=192.168.0.0/16 --apiserver-cert-extra-sans @@tenant-cluster-name@@
  - while true; do kubectl --kubeconfig=/etc/kubernetes/admin.conf version && break || sleep 5; done
  - kubectl --kubeconfig=/etc/kubernetes/admin.conf apply -f https://raw.githubusercontent.com/coreos/flannel/master/Documentation/kube-flannel.yml
  - kubectl --kubeconfig=/etc/kubernetes/admin.conf taint nodes --all node-role.kubernetes.io/master-
  - kubectl --kubeconfig=/root/infracluster.kubeconfig create cm tenant-config -n @@test-namespace@@ --from-file=/etc/kubernetes/admin.conf
  - mkdir -p /root/.kube
  - cp -i /etc/kubernetes/admin.conf /root/.kube/config
  - chown $(id -u):$(id -g) /root/.kube/config
`
