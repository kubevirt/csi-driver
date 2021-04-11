package functional_test

import (
	"flag"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/tools/clientcmd"
	"kubevirt.io/client-go/kubecli"
)

func TestFunctional(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Functional Suite")
}

func getInfraCluster() InfraCluster {
	client, err := kubecli.GetKubevirtClient()
	Expect(err).NotTo(HaveOccurred(), "Failed getting KubeVirt client")

	kubeconfig := flag.Lookup("kubeconfig").Value
	Expect(kubeconfig.String()).NotTo(BeEmpty())
	config := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig.String()},
		&clientcmd.ConfigOverrides{})

	infraCluster := InfraCluster{
		client,
		config,
		testNamespace,
	}
	return infraCluster
}
