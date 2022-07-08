module github.com/kubevirt/csi-driver/tests

go 1.15

require (
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/gomega v1.19.0
)

require (
	github.com/coreos/prometheus-operator v0.38.0 // indirect
	github.com/ghodss/yaml v1.0.1-0.20190212211648-25d852aebe32 // indirect
	github.com/go-openapi/spec v0.19.7 // indirect
	github.com/google/go-github/v32 v32.1.0 // indirect
	github.com/google/uuid v1.1.2
	github.com/kubevirt/csi-driver v0.0.0-20201203165039-02bba709abc7
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/openshift/api v0.0.0
	github.com/openshift/client-go v0.0.0
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
	k8s.io/api v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/cli-runtime v0.20.2
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/kubernetes v1.14.0
	kubevirt.io/client-go v0.39.1
	kubevirt.io/containerized-data-importer v1.26.1
	kubevirt.io/kubevirt v0.39.1
)

replace github.com/kubevirt/csi-driver => ../

replace github.com/go-kit/kit => github.com/go-kit/kit v0.3.0

replace (
	k8s.io/api => k8s.io/api v0.20.2
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.20.2
	k8s.io/apimachinery => k8s.io/apimachinery v0.20.2
	k8s.io/apiserver => k8s.io/apiserver v0.20.2
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.20.2
	k8s.io/client-go => k8s.io/client-go v0.20.2
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.20.2
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.20.2
	k8s.io/code-generator => k8s.io/code-generator v0.20.2
	k8s.io/component-base => k8s.io/component-base v0.20.2
	k8s.io/cri-api => k8s.io/cri-api v0.20.2
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.20.2
	k8s.io/klog => k8s.io/klog v0.4.0
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.20.2
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.20.2
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20210113233702-8566a335510f
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.20.2
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.20.2
	k8s.io/kubectl => k8s.io/kubectl v0.20.2
	k8s.io/kubelet => k8s.io/kubelet v0.20.2
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.20.2
	k8s.io/metrics => k8s.io/metrics v0.20.2
	k8s.io/node-api => k8s.io/node-api v0.20.2
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.20.2
	k8s.io/sample-cli-plugin => k8s.io/sample-cli-plugin v0.20.2
	k8s.io/sample-controller => k8s.io/sample-controller v0.20.2
	kubevirt.io/client-go => kubevirt.io/client-go v0.39.1

	kubevirt.io/containerized-data-importer => kubevirt.io/containerized-data-importer v1.26.1
)

replace (
	github.com/openshift/api => github.com/openshift/api v0.0.0-20210202165416-a9e731090f5e
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20210112165513-ebc401615f47
	github.com/operator-framework/operator-lifecycle-manager => github.com/operator-framework/operator-lifecycle-manager v0.17.1-0.20210204051820-4b67acc560a7
)

// sigs.k8s.io/controller-runtime 0.6.0 requires k8s-* v0.18.2 but we are pinned
// to kubernetes-1.16.4  as for kubevirt.io/kubevirt v0.33.0
// need github.com/operator-framework/api v0.3.5
//replace (
//	github.com/operator-framework/api => github.com/operator-framework/api v0.3.5
//	sigs.k8s.io/controller-runtime => sigs.k8s.io/controller-runtime v0.5.2
//)

// cluster-network-addons-operator pulls in dependency on operator-sdk 0.39.2
// but since HCO is pinned to Kubernetes v0.16.4, it needs to stay on operator-sdk
// v0.17.0.
//replace github.com/operator-framework/operator-sdk => github.com/operator-framework/operator-sdk v0.17.0

//replace github.com/docker/docker => github.com/moby/moby v0.7.3-0.20190826074503-38ab9da00309 // Required by Helm

// Pinned for compatibility with kubernetes-1.16.4
//replace sigs.k8s.io/controller-tools => sigs.k8s.io/controller-tools v0.2.4

//replace vbom.ml/util => github.com/fvbommel/util v0.0.0-20180919145318-efcd4e0f9787
//
//replace bitbucket.org/ww/goautoneg => github.com/munnerz/goautoneg v0.0.0-20120707110453-a547fc61f48d
