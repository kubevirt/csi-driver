package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	klog "k8s.io/klog/v2"

	"kubevirt.io/csi-driver/pkg/kubevirt"
	"kubevirt.io/csi-driver/pkg/service"
)

var (
	endpoint               = flag.String("endpoint", "unix:/csi/csi.sock", "CSI endpoint")
	nodeName               = flag.String("node-name", "", "The node name - the node this pods runs on")
	infraClusterNamespace  = flag.String("infra-cluster-namespace", "", "The infra-cluster namespace")
	infraClusterKubeconfig = flag.String("infra-cluster-kubeconfig", "", "the infra-cluster kubeconfig file. If not set, defaults to in cluster config.")
	infraClusterLabels     = flag.String("infra-cluster-labels", "", "The infra-cluster labels to use when creating resources in infra cluster. 'name=value' fields separated by a comma")

	tenantClusterKubeconfig = flag.String("tenant-cluster-kubeconfig", "", "the tenant cluster kubeconfig file. If not set, defaults to in cluster config.")

	runNodeService       = flag.Bool("run-node-service", true, "Specifies rather or not to run the node service, the default is true")
	runControllerService = flag.Bool("run-controller-service", true, "Specifies rather or not to run the controller service, the default is true")
)

func init() {
	err := flag.Set("logtostderr", "true")
	if err != nil {
		panic(fmt.Sprintf("can't set the logtostderr flags; %s", err.Error()))
	}
	// make glog and klog coexist
	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)

	// Sync the glog and klog flags.
	flag.CommandLine.Visit(func(f1 *flag.Flag) {
		f2 := klogFlags.Lookup(f1.Name)
		if f2 != nil {
			value := f1.Value.String()
			err = f2.Value.Set(value)
			if err != nil {
				panic(fmt.Sprintf("can't set the %s flags; %s", f1.Name, err.Error()))
			}
		}
	})
}

func main() {
	flag.Parse()
	rand.Seed(time.Now().UnixNano())
	handle()
	os.Exit(0)
}

func handle() {
	var tenantRestConfig *rest.Config
	var infraRestConfig *rest.Config
	var identityClientset *kubernetes.Clientset

	if service.VendorVersion == "" {
		klog.Fatalf("VendorVersion must be set at compile time")
	}
	klog.V(2).Infof("Driver vendor %v %v", service.VendorName, service.VendorVersion)

	inClusterConfig, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Failed to build in cluster config: %v", err)
	}

	if *tenantClusterKubeconfig != "" {
		tenantRestConfig, err = clientcmd.BuildConfigFromFlags("", *tenantClusterKubeconfig)
		if err != nil {
			klog.Fatalf("failed to build tenant cluster config: %v", err)
		}

	} else {
		tenantRestConfig = inClusterConfig
	}

	if *infraClusterKubeconfig != "" {
		infraRestConfig, err = clientcmd.BuildConfigFromFlags("", *infraClusterKubeconfig)
		if err != nil {
			klog.Fatalf("failed to build infra cluster config: %v", err)
		}

	} else {
		infraRestConfig = inClusterConfig
	}

	tenantClientSet, err := kubernetes.NewForConfig(tenantRestConfig)
	if err != nil {
		klog.Fatalf("Failed to build tenant client set: %v", err)
	}

	virtClient, err := kubevirt.NewClient(infraRestConfig)
	if err != nil {
		klog.Fatal(err)
	}

	var nodeID string
	if *nodeName != "" {
		node, err := tenantClientSet.CoreV1().Nodes().Get(context.TODO(), *nodeName, v1.GetOptions{})
		if err != nil {
			klog.Fatal(fmt.Errorf("failed to find node by name %v: %v", nodeName, err))
		}
		// systemUUID is the VM ID
		nodeID = node.Status.NodeInfo.SystemUUID
		klog.Infof("Node name: %v, Node ID: %s", nodeName, nodeID)
	}

	identityClientset = tenantClientSet
	if *runControllerService {
		identityClientset, err = kubernetes.NewForConfig(infraRestConfig)
		if err != nil {
			klog.Fatalf("Failed to build infra client set: %v", err)
		}
	}

	infraClusterLabelsMap := parseLabels()

	driver := service.NewKubevirtCSIDriver(virtClient,
		identityClientset,
		*infraClusterNamespace,
		infraClusterLabelsMap,
		nodeID,
		*runNodeService,
		*runControllerService)

	driver.Run(*endpoint)
}

func parseLabels() map[string]string {

	infraClusterLabelsMap := map[string]string{}

	if *infraClusterLabels == "" {
		return infraClusterLabelsMap
	}

	labelStrings := strings.Split(*infraClusterLabels, ",")

	for _, label := range labelStrings {

		labelPair := strings.SplitN(label, "=", 2)

		if len(labelPair) != 2 {
			panic("Bad labels format. Should be 'key=value,key=value,...'")
		}

		infraClusterLabelsMap[labelPair[0]] = labelPair[1]
	}

	return infraClusterLabelsMap
}
