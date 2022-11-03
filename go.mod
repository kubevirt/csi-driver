module kubevirt.io/csi-driver

go 1.18

require (
	github.com/container-storage-interface/spec v1.6.0
	github.com/go-bindata/go-bindata v3.1.2+incompatible
	github.com/golang/glog v1.0.0
	github.com/golang/mock v1.6.0
	github.com/golang/protobuf v1.5.2
	github.com/google/uuid v1.3.0
	github.com/kubernetes-csi/csi-lib-utils v0.11.0
	github.com/kubernetes-csi/csi-test/v5 v5.0.0
	github.com/onsi/ginkgo/v2 v2.1.4
	github.com/onsi/gomega v1.20.0
	github.com/openshift/build-machinery-go v0.0.0-20200713135615-1f43d26dccc7
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.7.0
	golang.org/x/net v0.0.0-20220802222814-0bcc04d9c69b
	google.golang.org/grpc v1.48.0
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.24.2
	k8s.io/apimachinery v0.24.2
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/klog/v2 v2.70.1
	k8s.io/utils v0.0.0-20220210201930-3a6ce19ff2f9
	kubevirt.io/api v0.55.0
	kubevirt.io/client-go v0.55.0
	kubevirt.io/containerized-data-importer-api v1.52.0
)

require (
	github.com/PuerkitoBio/purell v1.1.1 // indirect
	github.com/PuerkitoBio/urlesc v0.0.0-20170810143723-de5bf2ad4578 // indirect
	github.com/coreos/prometheus-operator v0.38.1-0.20200424145508-7e176fda06cc // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/emicklei/go-restful v2.15.0+incompatible // indirect
	github.com/evanphx/json-patch v4.12.0+incompatible // indirect
	github.com/fsnotify/fsnotify v1.5.1 // indirect
	github.com/go-kit/kit v0.10.0 // indirect
	github.com/go-logfmt/logfmt v0.5.0 // indirect
	github.com/go-logr/logr v1.2.3 // indirect
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.19.6 // indirect
	github.com/go-openapi/spec v0.20.3 // indirect
	github.com/go-openapi/swag v0.21.1 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/gnostic v0.5.7-v3refs // indirect
	github.com/google/go-cmp v0.5.8 // indirect
	github.com/google/gofuzz v1.1.0 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/imdario/mergo v0.3.12 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/k8snetworkplumbingwg/network-attachment-definition-client v0.0.0-20191119172530-79f836b90111 // indirect
	github.com/kubernetes-csi/external-snapshotter/client/v4 v4.2.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/openshift/api v0.0.0 // indirect
	github.com/openshift/client-go v0.0.0 // indirect
	github.com/openshift/custom-resource-status v1.1.2 // indirect
	github.com/pborman/uuid v1.2.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/oauth2 v0.0.0-20211104180415-d3ed0bb246c8 // indirect
	golang.org/x/sys v0.0.0-20220731174439-a90be440212d // indirect
	golang.org/x/term v0.0.0-20210927222741-03fcf44c2211 // indirect
	golang.org/x/text v0.3.7 // indirect
	golang.org/x/time v0.0.0-20220210224613-90d013bbcef8 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto v0.0.0-20220107163113-42d7afdf6368 // indirect
	google.golang.org/protobuf v1.28.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/apiextensions-apiserver v0.24.2 // indirect
	k8s.io/kube-openapi v0.0.0-20220328201542-3ee0da9b0b42 // indirect
	kubevirt.io/controller-lifecycle-operator-sdk/api v0.0.0-20220329064328-f3cc58c6ed90 // indirect
	sigs.k8s.io/json v0.0.0-20211208200746-9f7c6b3444d2 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.1 // indirect
	sigs.k8s.io/yaml v1.3.0 // indirect
)

replace (
	github.com/gogo/protobuf => github.com/gogo/protobuf v1.3.2
	github.com/opencontainers/runc => github.com/opencontainers/runc v1.1.3
	github.com/openshift/api => github.com/openshift/api v0.0.0-20191219222812-2987a591a72c
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20210112165513-ebc401615f47
	github.com/operator-framework/operator-lifecycle-manager => github.com/operator-framework/operator-lifecycle-manager v0.0.0-20190128024246-5eb7ae5bdb7a
	github.com/u-root/u-root => github.com/u-root/u-root v1.0.1
	gopkg.in/yaml.v2 => gopkg.in/yaml.v2 v2.4.0
	k8s.io/api => k8s.io/api v0.24.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.24.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.24.0
	k8s.io/apiserver => k8s.io/apiserver v0.24.0
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.24.0
	k8s.io/client-go => k8s.io/client-go v0.24.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.24.0
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.24.0
	k8s.io/code-generator => k8s.io/code-generator v0.24.0
	k8s.io/component-base => k8s.io/component-base v0.24.0
	k8s.io/component-helpers => k8s.io/component-helpers v0.24.0
	k8s.io/controller-manager => k8s.io/controller-manager v0.24.0
	k8s.io/cri-api => k8s.io/cri-api v0.24.0
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.24.0
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.24.0
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.24.0
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.24.0
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.24.0
	k8s.io/kubectl => k8s.io/kubectl v0.24.0
	k8s.io/kubelet => k8s.io/kubelet v0.24.0
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.24.0
	k8s.io/metrics => k8s.io/metrics v0.24.0
	k8s.io/mount-utils => k8s.io/mount-utils v0.24.0
	k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.24.0
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.24.0
)
