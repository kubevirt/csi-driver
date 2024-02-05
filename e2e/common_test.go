package e2e_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"
)

// RunCmd function executes a command, and returns STDOUT and STDERR bytes
func RunCmd(cmd *exec.Cmd) (stdoutBytes []byte, stderrBytes []byte) {
	// creates to bytes.Buffer, these are both io.Writer and io.Reader
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)

	// create the command and assign the outputs
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// run the command
	ExpectWithOffset(1, cmd.Run()).To(Succeed(), fmt.Sprintf("failed to run %s, with arguments: %v; error response: %s", cmd.Path, cmd.Args, stderr.Bytes()))

	return stdout.Bytes(), stderr.Bytes()
}

type tenantClusterAccess struct {
	listener             net.Listener
	namespace            string
	tenantKubeconfigFile string
	isForwarding         bool
	tenantApiport        int
}

func newTenantClusterAccess(namespace, tenantKubeconfigFile string, apiPort int) tenantClusterAccess {
	return tenantClusterAccess{
		namespace:            namespace,
		tenantKubeconfigFile: tenantKubeconfigFile,
		tenantApiport:        apiPort,
	}
}

func (t *tenantClusterAccess) generateTenantClient() (*kubernetes.Clientset, error) {
	overrides := &clientcmd.ConfigOverrides{}
	if _, err := os.Stat(t.tenantKubeconfigFile); errors.Is(err, os.ErrNotExist) {
		localPort := t.listener.Addr().(*net.TCPAddr).Port
		cmd := exec.Command(ClusterctlPath, "get", "kubeconfig", "kvcluster",
			"--namespace", t.namespace)
		stdout, _ := RunCmd(cmd)
		if err := os.WriteFile(t.tenantKubeconfigFile, stdout, 0644); err != nil {
			return nil, err
		}
		overrides = &clientcmd.ConfigOverrides{
			ClusterInfo: clientcmdapi.Cluster{
				Server:                fmt.Sprintf("https://127.0.0.1:%d", localPort),
				InsecureSkipTLSVerify: true,
			},
		}
	}
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: t.tenantKubeconfigFile}, overrides)
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(restConfig)
}

func (t *tenantClusterAccess) startForwardingTenantAPI() error {
	if t.isForwarding {
		return nil
	}
	address, err := net.ResolveIPAddr("", "127.0.0.1")
	if err != nil {
		return err
	}
	t.listener, err = net.ListenTCP(
		"tcp",
		&net.TCPAddr{
			IP:   address.IP,
			Zone: address.Zone,
		})
	if err != nil {
		return err
	}

	t.isForwarding = true
	go t.waitForConnection()

	return nil
}

func (t *tenantClusterAccess) stopForwardingTenantAPI() error {
	if !t.isForwarding {
		return nil
	}
	t.isForwarding = false
	return t.listener.Close()
}

func (t *tenantClusterAccess) waitForConnection() {
	conn, err := t.listener.Accept()
	if err != nil {
		klog.Errorln("error accepting connection:", err)
		return
	}

	proxy, err := net.Dial("tcp", net.JoinHostPort("localhost", strconv.Itoa(t.tenantApiport)))
	if err != nil {
		klog.Errorf("unable to connect to local port-forward: %v", err)
		return
	}
	go t.handleConnection(conn, proxy)
}

// handleConnection copies data between the local connection and the stream to
// the remote server. It closes the local and remote connections when done.
func (t *tenantClusterAccess) handleConnection(local, remote io.ReadWriteCloser) {
	defer local.Close()
	defer remote.Close()
	errs := make(chan error, 2)
	go func() {
		_, err := io.Copy(remote, local)
		errs <- err
	}()
	go func() {
		_, err := io.Copy(local, remote)
		errs <- err
	}()

	t.handleConnectionError(<-errs)
}

func (t *tenantClusterAccess) handleConnectionError(err error) {
	if err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
		klog.Errorf("error handling portForward connection: %v", err)
	}
}

func generateInfraRestConfig() (*rest.Config, error) {
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: InfraKubeConfig}, &clientcmd.ConfigOverrides{})
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}
	return restConfig, nil
}

func generateInfraClient() (*kubernetes.Clientset, error) {
	restConfig, err := generateInfraRestConfig()
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(restConfig)
}
