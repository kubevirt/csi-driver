package e2e_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	kubevirtv1 "kubevirt.io/api/core/v1"
	kubecli "kubevirt.io/csi-driver/pkg/generated/kubevirt/client-go/clientset/versioned"

	"flag"
	"testing"
)

// Test suite required arguments
var (
	KubectlPath           string
	ClusterctlPath        string
	VirtctlPath           string
	DumpPath              string
	WorkingDir            string
	TenantKubeConfig      string
	InfraClusterNamespace string
	InfraKubeConfig       string
	VolumeSnapshotClass   string
	cancelFunc            func() error
	tenantApiPort         int
)

// Initialize test required arguments
func init() {
	flag.StringVar(&KubectlPath, "kubectl-path", "", "Path to the kubectl binary")
	flag.StringVar(&VirtctlPath, "virtctl-path", "", "Path to the virtctl binary")
	flag.StringVar(&ClusterctlPath, "clusterctl-path", "", "Path to the clusterctl binary")
	flag.StringVar(&DumpPath, "dump-path", "", "Path to the kubevirt artifacts dump cmd binary")
	flag.StringVar(&WorkingDir, "working-dir", "", "Path used for e2e test files")
	flag.StringVar(&TenantKubeConfig, "tenant-kubeconfig", "", "Path to tenant kubeconfig")
	flag.StringVar(&InfraKubeConfig, "infra-kubeconfig", "", "Path to infra kubeconfig")
	flag.StringVar(&InfraClusterNamespace, "infra-cluster-namespace", "kv-guest-cluster", "Namespace of the guest cluster in the infra cluster")
	flag.StringVar(&VolumeSnapshotClass, "volume-snapshot-class", "kubevirt-csi-snapclass", "Name of the volume snapshot class")
}

func TestE2E(t *testing.T) {
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
	if VirtctlPath == "" {
		t.Fatal("virtctl-path required")
	} else if _, err := os.Stat(VirtctlPath); os.IsNotExist(err) {
		t.Fatalf("invalid virtctl-path path: %s doesn't exist", VirtctlPath)
	}
	if WorkingDir == "" {
		t.Fatal("working-dir required")
	} else if _, err := os.Stat(WorkingDir); os.IsNotExist(err) {
		t.Fatalf("invalid working-dir path: %s doesn't exist", WorkingDir)
	}
	if DumpPath != "" {
		if _, err := os.Stat(DumpPath); os.IsNotExist(err) {
			t.Fatalf("invalid dump-path: %s doesn't exist", DumpPath)
		}
	}

	if len(InfraKubeConfig) == 0 {
		t.Fatal("infra kubeconfig required")
	}

	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Suite")
}

var _ = BeforeSuite(func() {
	// parse test suite arguments
	flag.Parse()
	if err := startPortForward(InfraClusterNamespace); err != nil {
		klog.Fatalf("failed to start port-forward: %v", err)
	}
})

var _ = AfterSuite(func() {
	if cancelFunc != nil {
		if err := cancelFunc(); err != nil {
			klog.Errorf("failed to cancel port-forward: %v", err)
		}
	}
})

var _ = JustAfterEach(func() {
	if CurrentSpecReport().Failed() && DumpPath != "" {
		dump(os.Getenv("KUBECONFIG"), "")
	}
})

func dump(kubeconfig, artifactsSuffix string) {
	cmd := exec.Command(DumpPath, "--kubeconfig", kubeconfig)

	failureLocation := CurrentSpecReport().Failure.Location
	artifactsPath := filepath.Join(os.Getenv("ARTIFACTS"), fmt.Sprintf("%s:%d", filepath.Base(failureLocation.FileName), failureLocation.LineNumber), artifactsSuffix)
	cmd.Env = append(cmd.Env, fmt.Sprintf("ARTIFACTS=%s", artifactsPath))

	By(fmt.Sprintf("dumping k8s artifacts to %s", artifactsPath))
	output, err := cmd.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), string(output))
}

func startPortForward(namespace string) error {
	name, err := findControlPlaneVMIName(namespace)
	if err != nil {
		klog.Errorf("error finding control plane vmi in namespace %s, %v\n", namespace, err)
		return err
	}
	tenantApiPort, err = getFreePort()
	if err != nil {
		return err
	}
	fmt.Printf("Found free port: %d\n", tenantApiPort)
	cmd := CreateVirtctlCommand(context.Background(), "port-forward", "-n", namespace, fmt.Sprintf("vm/%s", name), fmt.Sprintf("%d:6443", tenantApiPort))
	cancelFunc = cmd.Cancel
	if cancelFunc == nil {
		klog.Errorln("unable to get cancel function for port-forward command")
		return nil
	}

	go func() {
		out, err := cmd.CombinedOutput()
		if err != nil {
			klog.Errorf("error port-forwarding vmi %s/%s: %v, [%s]\n", namespace, name, err, string(out))
			return
		}
	}()
	return nil
}

func findControlPlaneVMIName(namespace string) (string, error) {
	clientConfig := defaultInfraClientConfig(&pflag.FlagSet{})
	cfg, err := clientConfig.ClientConfig()
	Expect(err).ToNot(HaveOccurred())
	virtClient = kubecli.NewForConfigOrDie(cfg)
	Expect(err).ToNot(HaveOccurred())
	vmiList, err := virtClient.KubevirtV1().VirtualMachineInstances(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return "", err
	}

	var chosenVMI *kubevirtv1.VirtualMachineInstance
	for _, vmi := range vmiList.Items {
		if strings.Contains(vmi.Name, "-control-plane") {
			chosenVMI = &vmi
			break
		}
	}
	if chosenVMI == nil {
		return "", fmt.Errorf("Couldn't find controlplane vmi in namespace %s", namespace)
	}
	return chosenVMI.Name, nil
}

func getFreePort() (port int, err error) {
	var a *net.TCPAddr
	if a, err = net.ResolveTCPAddr("tcp", "localhost:0"); err == nil {
		var l *net.TCPListener
		if l, err = net.ListenTCP("tcp", a); err == nil {
			defer l.Close()
			return l.Addr().(*net.TCPAddr).Port, nil
		}
	}
	return
}

func CreateVirtctlCommand(ctx context.Context, args ...string) *exec.Cmd {
	kubeconfig := InfraKubeConfig
	path := VirtctlPath

	klog.Infof("%s %v\n", path, args)
	cmd := exec.CommandContext(ctx, path, args...)
	klog.Infof("Kubeconfig: %s\n", kubeconfig)
	kubeconfEnv := fmt.Sprintf("KUBECONFIG=%s", kubeconfig)
	cmd.Env = append(os.Environ(), kubeconfEnv)
	klog.Flush()
	return cmd
}
