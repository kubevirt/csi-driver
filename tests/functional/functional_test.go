package functional

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	routesclientset "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/yaml"
	apiwatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	clientwatch "k8s.io/client-go/tools/watch"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"kubevirt.io/csi-driver/pkg/generated"
)

var infraCluster InfraCluster
var tenantCluster TenantCluster

var _ = BeforeSuite(func() {
	infraCluster = getInfraCluster()
	Expect(PrepareEnv(infraCluster)).To(Succeed())
})

var _ = AfterSuite(func() {
	TearDownEnv(infraCluster)
})

var _ = Describe("KubeVirt CSI Driver functional tests", func() {

	BeforeEach(func() {
		tenantCluster = getTenantCluster(infraCluster)
	})

	Context("deployed on vanilla k8s", func() {

		It("Deploys the CSI driver components", func() {
			deployDriver(infraCluster, tenantCluster)
			deploySecretWithInfraDetails(infraCluster, tenantCluster)
		})

		It("Deploys a pod consuming a PV by PVC", func() {
			deployStorageClass(tenantCluster)
			deployPVC(tenantCluster)
			// create pod check pod starts
			err := deployTestPod(tenantCluster)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Destroys the test pod", func() {
			err := destroyTestPod(tenantCluster)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Destroys the PVC", func() {
			// delete pvc, see disk is detached and deleted
			err := destroyPVC(tenantCluster)
			Expect(err).NotTo(HaveOccurred())
		})

		It("DV is unplugged from the VMI", func() {
			// check the infra DV is hot unplug
			_, err := watchDVUnpluged(&infraCluster)
			Expect(err).NotTo(HaveOccurred())
		})
		//todo test the dv is deleted from infracluster
	})
})

func getTenantCluster(c InfraCluster) TenantCluster {
	waitForTenant(c)
	// get the tenant kubeconfig
	tenantConfigMap, err := c.virtCli.CoreV1().
		ConfigMaps(c.namespace).Get(context.Background(), tenantConfigName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())

	tenantExternalHostname := getTenantExternalHostname(c)

	tenantCluster, err := getTenantClusterSetup(tenantConfigMap, tenantExternalHostname)
	Expect(err).NotTo(HaveOccurred())
	return *tenantCluster
}

func getTenantExternalHostname(c InfraCluster) string {
	if IsOpenShift() {
		routes, err := routesclientset.NewForConfig(c.virtCli.Config())
		Expect(err).NotTo(HaveOccurred())
		route, err := routes.Routes(c.namespace).Get(context.Background(), routeName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		return route.Spec.Host
	}
	get, err := c.virtCli.NetworkingV1().Ingresses(c.namespace).Get(context.Background(), routeName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	return get.Spec.Rules[0].Host
}

func waitForTenant(c InfraCluster) {
	fmt.Fprintf(GinkgoWriter, "Wait for tenant to be up... ")
	// wait till the config map is set by the tenant. This means the install is successful
	// and we can extract the kubeconfig
	tenantUpTimeout, cancelFunc := context.WithTimeout(context.Background(), time.Minute*40)
	defer cancelFunc()
	_, _ = clientwatch.UntilWithSync(
		tenantUpTimeout,
		cache.NewListWatchFromClient(c.virtCli.CoreV1().RESTClient(), "configmaps",
			c.namespace, fields.OneTermEqualSelector("metadata.name", tenantConfigName)),
		&v1.ConfigMap{},
		nil,
		func(event apiwatch.Event) (bool, error) {
			switch event.Type {
			case apiwatch.Added:
			default:
				return false, nil
			}
			_, ok := event.Object.(*v1.ConfigMap)
			if !ok {
				Fail("couldn't find config map 'tenant-config' in the namespace. Tenant cluster might failed installation")
			}

			fmt.Fprintf(GinkgoWriter, "up and running\n")
			return true, nil
		},
	)
}

func deployDriver(c InfraCluster, tenantSetup TenantCluster) {
	// 1. create ServiceAccount on infra
	_, err := c.createObject(
		*c.virtCli.Config(),
		c.virtCli.Discovery(),
		c.namespace,
		bytes.NewReader(generated.MustAsset("deploy/infra-cluster-service-account.yaml")))
	if !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
	// 2. create the rest of deploy/0xx-.yaml files
	assets := generated.AssetNames()
	sort.Strings(assets)
	for _, a := range assets {
		if !strings.HasPrefix(a, "deploy/0") {
			continue
		}
		fmt.Fprintf(GinkgoWriter, "Deploying to tenant %s\n", a)
		_, err := c.createObject(
			*tenantSetup.restConfig,
			tenantSetup.client.Discovery(),
			tenantSetup.namespace,
			bytes.NewReader(generated.MustAsset(a)),
		)
		if !errors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("deploying %s should not have failed. Error %v", a, err))
		}
	}
}

func destroyPVC(tenantSetup TenantCluster) error {
	pvc := v1.PersistentVolumeClaim{}
	pvcData := bytes.NewReader(generated.MustAsset("deploy/example/storage-claim.yaml"))
	err := yaml.NewYAMLToJSONDecoder(pvcData).Decode(&pvc)
	if err != nil {
		return err
	}
	return tenantSetup.client.CoreV1().
		PersistentVolumeClaims(tenantSetup.namespace).Delete(context.Background(), pvc.Name, metav1.DeleteOptions{})
}

func deploySecretWithInfraDetails(c InfraCluster, tenantSetup TenantCluster) {
	s := v1.Secret{}
	secretData := bytes.NewReader(generated.MustAsset("deploy/secret.yaml"))
	err := yaml.NewYAMLToJSONDecoder(secretData).Decode(&s)
	Expect(err).NotTo(HaveOccurred())

	s.Data = make(map[string][]byte)
	infraKubeconfig, err := c.kubeconfig.RawConfig()
	Expect(err).NotTo(HaveOccurred())
	kubeconfigData, err := clientcmd.Write(infraKubeconfig)
	Expect(err).NotTo(HaveOccurred())
	s.Data["kubeconfig"] = kubeconfigData
	_, err = tenantSetup.client.CoreV1().Secrets(tenantSetup.namespace).Create(context.Background(), &s, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}

	cm := v1.ConfigMap{}
	cmData := bytes.NewReader(generated.MustAsset("deploy/configmap.yaml"))
	err = yaml.NewYAMLToJSONDecoder(cmData).Decode(&cm)
	Expect(err).NotTo(HaveOccurred())
	cm.Data["infraClusterNamespace"] = c.namespace
	_, err = tenantSetup.client.CoreV1().ConfigMaps(tenantSetup.namespace).Create(context.Background(), &cm, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

func deployStorageClass(tenantSetup TenantCluster) {
	sc := storagev1.StorageClass{}
	storageClassData := bytes.NewReader(generated.MustAsset("deploy/example/storageclass.yaml"))
	err := yaml.NewYAMLToJSONDecoder(storageClassData).Decode(&sc)
	Expect(err).NotTo(HaveOccurred())
	_, err = tenantSetup.client.StorageV1().StorageClasses().Create(context.Background(), &sc, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

func deployPVC(tenantSetup TenantCluster) {
	pvc := v1.PersistentVolumeClaim{}
	pvcData := bytes.NewReader(generated.MustAsset("deploy/example/storage-claim.yaml"))
	err := yaml.NewYAMLToJSONDecoder(pvcData).Decode(&pvc)
	Expect(err).NotTo(HaveOccurred())
	_, err = tenantSetup.client.CoreV1().PersistentVolumeClaims(tenantSetup.namespace).Create(context.Background(), &pvc, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

func deployTestPod(tenantSetup TenantCluster) error {
	testPod := v1.Pod{}
	podData := bytes.NewReader(generated.MustAsset("deploy/example/test-pod.yaml"))
	err := yaml.NewYAMLToJSONDecoder(podData).Decode(&testPod)
	Expect(err).NotTo(HaveOccurred())
	_, err = tenantSetup.client.CoreV1().Pods(tenantSetup.namespace).Create(context.Background(), &testPod, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}

	testPodUpTimeout, cancelFunc := context.WithTimeout(context.Background(), time.Minute*20)
	defer cancelFunc()

	_, err = clientwatch.UntilWithSync(
		testPodUpTimeout,
		cache.NewListWatchFromClient(tenantSetup.client.CoreV1().RESTClient(), "pods",
			tenantSetup.namespace, fields.OneTermEqualSelector("metadata.name", testPod.Name)),
		&v1.Pod{},
		nil,
		func(event apiwatch.Event) (bool, error) {
			switch event.Type {
			case apiwatch.Added, apiwatch.Modified:
				p, ok := event.Object.(*v1.Pod)
				if !ok {
					Fail("Failed fetching the test pod")
				}
				if p.Status.Phase == v1.PodRunning {
					return true, nil
				}
			default:
				return false, nil
			}

			return false, nil
		},
	)

	Expect(err).ToNot(HaveOccurred())

	return nil
}

func destroyTestPod(tenantSetup TenantCluster) error {
	testPod := v1.Pod{}
	podData := bytes.NewReader(generated.MustAsset("deploy/example/test-pod.yaml"))
	err := yaml.NewYAMLToJSONDecoder(podData).Decode(&testPod)
	Expect(err).NotTo(HaveOccurred())
	return tenantSetup.client.CoreV1().Pods(tenantSetup.namespace).Delete(context.Background(), testPod.Name, metav1.DeleteOptions{})
}

func getTenantClusterSetup(cm *v1.ConfigMap, tenantExternalHostname string) (*TenantCluster, error) {
	// substitute the internal API IP with the ingress route
	tenantConfigData := cm.Data["admin.conf"]
	tenantConfig, err := clientcmd.Load([]byte(tenantConfigData))
	if err != nil {
		return nil, err
	}
	tenantConfig.Clusters["kubernetes"].Server = fmt.Sprintf("https://%s", tenantExternalHostname)
	if err != nil {
		return nil, err
	}
	config, err := clientcmd.NewDefaultClientConfig(*tenantConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, err
	}
	tenantClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	version, err := tenantClient.ServerVersion()
	fmt.Fprintf(GinkgoWriter, "Tenant server version %v\n", version)
	Expect(err).NotTo(HaveOccurred())

	return &TenantCluster{
		namespace:  "kubevirt-csi-driver",
		config:     tenantConfig,
		restConfig: config,
		client:     tenantClient,
	}, nil
}

func watchDVUnpluged(c *InfraCluster) (*apiwatch.Event, error) {
	testDVUnplugTimeout, cancelFunc := context.WithTimeout(context.Background(), time.Minute*10)
	defer cancelFunc()

	return clientwatch.UntilWithSync(
		testDVUnplugTimeout,
		cache.NewListWatchFromClient(c.virtCli.RestClient(), "virtualmachineinstances",
			c.namespace, fields.OneTermEqualSelector("metadata.name", k8sMachineName)),
		&kubevirtv1.VirtualMachineInstance{},
		nil,
		func(event apiwatch.Event) (bool, error) {
			switch event.Type {
			case apiwatch.Added, apiwatch.Modified:
				vm, ok := event.Object.(*kubevirtv1.VirtualMachineInstance)
				if !ok {
					Fail("couldn't detect a change in VMI " + k8sMachineName)
				}
				for _, vs := range vm.Status.VolumeStatus {
					if vs.HotplugVolume != nil && vs.HotplugVolume.AttachPodName != "" {
						// we have an attached volume - fail
						return false, nil
					}
				}
			default:
				return false, nil
			}

			// no more attached volumes.
			return true, nil
		},
	)
}
