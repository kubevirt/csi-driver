package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8sv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"flag"
	"testing"
)

// Test suite required arguments
var (
	KubectlPath           string
	ClusterctlPath        string
	WorkingDir            string
	TenantKubeConfig      string
	InfraClusterNamespace string
	InfraKubeConfig       string
)

// Initialize test required arguments
func init() {
	flag.StringVar(&KubectlPath, "kubectl-path", "", "Path to the kubectl binary")
	flag.StringVar(&ClusterctlPath, "clusterctl-path", "", "Path to the clusterctl binary")
	flag.StringVar(&WorkingDir, "working-dir", "", "Path used for e2e test files")
	flag.StringVar(&TenantKubeConfig, "tenant-kubeconfig", "", "Path to tenant kubeconfig")
	flag.StringVar(&InfraKubeConfig, "infra-kubeconfig", "", "Path to infra kubeconfig")
	flag.StringVar(&InfraClusterNamespace, "infra-cluster-namespace", "kv-guest-cluster", "Namespace of the guest cluster in the infra cluster")
}

func TestE2E(t *testing.T) {
	if len(TenantKubeConfig) == 0 {
		// Make sure that valid arguments have been passed for this test suite run.
		if KubectlPath == "" {
			t.Fatal("kubectl-path or tenant-kubeconfig required")
		} else if _, err := os.Stat(KubectlPath); os.IsNotExist(err) {
			t.Fatalf("invalid kubectl-path path: %s doesn't exist", KubectlPath)
		}
		if ClusterctlPath == "" {
			t.Fatal("clusterctl-path required")
		} else if _, err := os.Stat(ClusterctlPath); os.IsNotExist(err) {
			t.Fatalf("invalid clusterctl-path path: %s doesn't exist", ClusterctlPath)
		}
		if WorkingDir == "" {
			t.Fatal("working-dir required")
		} else if _, err := os.Stat(WorkingDir); os.IsNotExist(err) {
			t.Fatalf("invalid working-dir path: %s doesn't exist", WorkingDir)
		}
	}

	if len(InfraKubeConfig) == 0 {
		t.Fatal("infra kubeconfig required")
	}

	cleanupArtifacts(os.Getenv("ARTIFACTS"))
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Suite")
}

var _ = BeforeSuite(func() {
	// parse test suite arguments
	flag.Parse()
})

var _ = JustAfterEach(func() {
	if CurrentSpecReport().Failed() {
		NewKubernetesReporter().Dump(CurrentSpecReport().RunTime, CurrentSpecReport().LeafNodeText)
	}
})

// KubernetesReporter is the struct that holds the report info.
type KubernetesReporter struct {
	FailureCount int
	artifactsDir string
	tenantClient *kubernetes.Clientset
	infraClient  *kubernetes.Clientset
}

// NewKubernetesReporter creates a new instance of the reporter.
func NewKubernetesReporter() *KubernetesReporter {
	return &KubernetesReporter{
		artifactsDir: os.Getenv("ARTIFACTS"),
	}
}

// Dump dumps the current state of the cluster. The relevant logs are collected starting
// from the since parameter.
func (r *KubernetesReporter) Dump(since time.Duration, testName string) {
	// If we got no directory, don't dump
	if r.artifactsDir == "" {
		fmt.Fprintf(GinkgoWriter, "No artifacts directory specified, not dumping cluster state")
		return
	}
	r.FailureCount = r.FailureCount + 1
	node := GinkgoParallelProcess()
	basePath := filepath.Join(r.artifactsDir, fmt.Sprintf("%d", node), testName)
	// Can call this as many times as needed, if the directory exists, nothing happens.
	if err := os.MkdirAll(basePath, 0777); err != nil {
		fmt.Fprintf(GinkgoWriter, "failed to create directory: %s, %v\n", basePath, err)
		return
	}
	r.createTenantClient()
	r.createInfraClient()

	infraPath := filepath.Join(basePath, "infra")
	if err := os.MkdirAll(infraPath, 0777); err != nil {
		fmt.Fprintf(GinkgoWriter, "failed to create directory: %v\n", err)
		return
	}
	tenantPath := filepath.Join(basePath, "tenant")
	if err := os.MkdirAll(tenantPath, 0777); err != nil {
		fmt.Fprintf(GinkgoWriter, "failed to create directory: %v\n", err)
		return
	}
	r.exportEventsJSON(r.infraClient, infraPath, since)
	r.exportEventsJSON(r.tenantClient, tenantPath, since)
	r.exportNodesJSON(r.infraClient, infraPath)
	r.exportNodesJSON(r.tenantClient, tenantPath)
	r.exportPVCsJSON(r.infraClient, infraPath)
	r.exportPVCsJSON(r.tenantClient, tenantPath)
	r.exportPVsJSON(r.infraClient, infraPath)
	r.exportPVsJSON(r.tenantClient, tenantPath)
	r.exportPodsJSON(r.infraClient, infraPath)
	r.exportPodsJSON(r.tenantClient, tenantPath)
	r.exportServicesJSON(r.infraClient, infraPath)
	r.exportServicesJSON(r.tenantClient, tenantPath)
	r.exportEndpointsJSON(r.infraClient, infraPath)
	r.exportEndpointsJSON(r.tenantClient, tenantPath)
	r.exportCSIDriversJSON(r.infraClient, infraPath)
	r.exportCSIDriversJSON(r.tenantClient, tenantPath)
	r.exportVMIJSON(InfraClusterNamespace, infraPath)
	r.podLogs(r.infraClient, infraPath, since)
	r.podLogs(r.tenantClient, tenantPath, since)
}

// cleanupArtifacts cleans up the current content of the artifactsDir
func cleanupArtifacts(artifactsDir string) {
	// clean up artifacts from previous run
	if artifactsDir != "" {
		os.RemoveAll(artifactsDir)
	}
}

func (r *KubernetesReporter) createTenantClient() {
	var tenantAccessor tenantClusterAccess
	if len(TenantKubeConfig) == 0 {
		tmpDir := os.TempDir()
		tenantKubeconfigFile := filepath.Join(tmpDir, fmt.Sprintf("tenant-kubeconfig_%d.yaml", GinkgoParallelProcess()))
		fmt.Fprintf(GinkgoWriter, "tenant kubeconfig file %s\n", tenantKubeconfigFile)
		tenantAccessor = newTenantClusterAccess("kvcluster", tenantKubeconfigFile)
		err := tenantAccessor.startForwardingTenantAPI()
		if err != nil {
			fmt.Fprintf(GinkgoWriter, "error forwarding %v\n", err)
		}
	} else {
		tenantAccessor = newTenantClusterAccess(InfraClusterNamespace, TenantKubeConfig)
	}
	var err error
	r.tenantClient, err = tenantAccessor.generateClient()
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "unable to create tenant client, %v", err)
	}
}

func (r *KubernetesReporter) createInfraClient() {
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: InfraKubeConfig}, &clientcmd.ConfigOverrides{})
	restConfig, err := clientConfig.ClientConfig()
	Expect(err).ToNot(HaveOccurred())

	r.infraClient, err = kubernetes.NewForConfig(restConfig)
	Expect(err).ToNot(HaveOccurred())
}

func (r *KubernetesReporter) exportPodsJSON(client *kubernetes.Clientset, path string) {
	f, err := os.OpenFile(filepath.Join(path, "pods.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open the file: %v", err)
		return
	}
	defer f.Close()

	pods, err := client.CoreV1().Pods(k8sv1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch pods: %v\n", err)
		return
	}

	j, err := json.MarshalIndent(pods, "", "    ")
	if err != nil {
		return
	}
	fmt.Fprintln(f, string(j))
}

func (r *KubernetesReporter) exportServicesJSON(client *kubernetes.Clientset, path string) {
	f, err := os.OpenFile(filepath.Join(path, "services.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open the file: %v", err)
		return
	}
	defer f.Close()

	services, err := client.CoreV1().Services(k8sv1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch services: %v\n", err)
		return
	}

	j, err := json.MarshalIndent(services, "", "    ")
	if err != nil {
		return
	}
	fmt.Fprintln(f, string(j))
}

func (r *KubernetesReporter) exportEndpointsJSON(client *kubernetes.Clientset, path string) {
	f, err := os.OpenFile(filepath.Join(path, "endpoints.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open the file: %v", err)
		return
	}
	defer f.Close()

	endpoints, err := client.CoreV1().Endpoints(k8sv1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch endpointss: %v\n", err)
		return
	}

	j, err := json.MarshalIndent(endpoints, "", "    ")
	if err != nil {
		return
	}
	fmt.Fprintln(f, string(j))
}

func (r *KubernetesReporter) exportNodesJSON(client *kubernetes.Clientset, path string) {
	f, err := os.OpenFile(filepath.Join(path, "nodes.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open the file: %v\n", err)
		return
	}
	defer f.Close()

	nodes, err := client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch nodes: %v\n", err)
		return
	}

	j, err := json.MarshalIndent(nodes, "", "    ")
	if err != nil {
		return
	}
	fmt.Fprintln(f, string(j))
}

func (r *KubernetesReporter) exportPVsJSON(client *kubernetes.Clientset, path string) {
	f, err := os.OpenFile(filepath.Join(path, "pvs.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open the file: %v\n", err)
		return
	}
	defer f.Close()

	pvs, err := client.CoreV1().PersistentVolumes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch pvs: %v\n", err)
		return
	}

	j, err := json.MarshalIndent(pvs, "", "    ")
	if err != nil {
		return
	}
	fmt.Fprintln(f, string(j))
}

func (r *KubernetesReporter) exportPVCsJSON(client *kubernetes.Clientset, path string) {
	f, err := os.OpenFile(filepath.Join(path, "pvcs.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open the file: %v\n", err)
		return
	}
	defer f.Close()

	pvcs, err := client.CoreV1().PersistentVolumeClaims(k8sv1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch pvcs: %v\n", err)
		return
	}

	j, err := json.MarshalIndent(pvcs, "", "    ")
	if err != nil {
		return
	}
	fmt.Fprintln(f, string(j))
}

func (r *KubernetesReporter) exportCSIDriversJSON(client *kubernetes.Clientset, path string) {
	f, err := os.OpenFile(filepath.Join(path, "csidrivers.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open the file: %v\n", err)
		return
	}
	defer f.Close()

	csiDrivers, err := client.StorageV1().CSIDrivers().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch csidrivers: %v\n", err)
		return
	}

	j, err := json.MarshalIndent(csiDrivers, "", "    ")
	if err != nil {
		return
	}
	fmt.Fprintln(f, string(j))
}

func (r *KubernetesReporter) exportVMIJSON(namespace, path string) {
	f, err := os.OpenFile(filepath.Join(path, "vmis.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open the file: %v\n", err)
		return
	}
	defer f.Close()

	vmis, err := virtClient.VirtualMachineInstance(namespace).List(context.Background(), &metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch vmis: %v\n", err)
		return
	}

	j, err := json.MarshalIndent(vmis, "", "    ")
	if err != nil {
		return
	}
	fmt.Fprintln(f, string(j))
}

func (r *KubernetesReporter) podLogs(client *kubernetes.Clientset, path string, since time.Duration) {
	logsdir := filepath.Join(path, "pods")

	if err := os.MkdirAll(logsdir, 0777); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create directory: %v\n", err)
		return
	}

	startTime := time.Now().Add(-since).Add(-5 * time.Second)

	pods, err := client.CoreV1().Pods(k8sv1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to fetch pods: %v\n", err)
		return
	}

	for _, pod := range pods.Items {
		for _, container := range pod.Spec.Containers {
			current, err := os.OpenFile(filepath.Join(logsdir, fmt.Sprintf("%d_%s_%s-%s.log", r.FailureCount, pod.Namespace, pod.Name, container.Name)), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to open the file: %v\n", err)
				return
			}
			defer current.Close()

			previous, err := os.OpenFile(filepath.Join(logsdir, fmt.Sprintf("%d_%s_%s-%s_previous.log", r.FailureCount, pod.Namespace, pod.Name, container.Name)), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to open the file: %v\n", err)
				return
			}
			defer previous.Close()

			logStart := metav1.NewTime(startTime)
			logs, err := client.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &k8sv1.PodLogOptions{SinceTime: &logStart, Container: container.Name}).DoRaw(context.TODO())
			if err == nil {
				fmt.Fprintln(current, string(logs))
			}

			logs, err = client.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &k8sv1.PodLogOptions{SinceTime: &logStart, Container: container.Name, Previous: true}).DoRaw(context.TODO())
			if err == nil {
				fmt.Fprintln(previous, string(logs))
			}
		}
	}
}

func (r *KubernetesReporter) exportEventsJSON(client *kubernetes.Clientset, path string, since time.Duration) {
	f, err := os.OpenFile(filepath.Join(path, "events.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open the infra event file: %v\n", err)
		return
	}
	defer f.Close()

	startTime := time.Now().Add(-since).Add(-5 * time.Second)

	// Infra events
	events, err := client.CoreV1().Events(k8sv1.NamespaceAll).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return
	}

	e := events.Items
	sort.Slice(e, func(i, j int) bool {
		return e[i].LastTimestamp.After(e[j].LastTimestamp.Time)
	})

	eventsToPrint := k8sv1.EventList{}
	for _, event := range e {
		if event.LastTimestamp.Time.After(startTime) {
			eventsToPrint.Items = append(eventsToPrint.Items, event)
		}
	}

	j, err := json.MarshalIndent(eventsToPrint, "", "    ")
	if err != nil {
		return
	}
	fmt.Fprintln(f, string(j))
}
