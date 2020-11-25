module github.com/kubevirt/csi-driver

go 1.15

require (
	github.com/container-storage-interface/spec v1.3.0
	github.com/go-kit/kit v0.10.0 // indirect
	github.com/golang/mock v1.4.4
	github.com/golang/protobuf v1.4.2
	github.com/kubernetes-csi/csi-lib-utils v0.7.0
	github.com/onsi/ginkgo v1.12.1
	github.com/onsi/gomega v1.10.1
	github.com/pkg/errors v0.9.1
	github.com/stretchr/testify v1.6.1
	golang.org/x/net v0.0.0-20200602114024-627f9648deb9
	google.golang.org/grpc v1.27.0
	k8s.io/api v0.19.0-rc.2
	k8s.io/apimachinery v0.19.0-rc.2
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/klog v1.0.0
	k8s.io/utils v0.0.0-20200720150651-0bdb4ca86cbc
	kubevirt.io/client-go v0.35.0-rc.0
	kubevirt.io/containerized-data-importer v1.25.0
)

replace (
	github.com/go-kit/kit => github.com/go-kit/kit v0.3.0
	github.com/go-openapi/spec => github.com/go-openapi/spec v0.19.3
	github.com/googleapis/gnostic => github.com/googleapis/gnostic v0.3.1
	github.com/openshift/api => github.com/openshift/api v0.0.0-20191219222812-2987a591a72c
	github.com/openshift/client-go => github.com/openshift/client-go v0.0.0-20191125132246-f6563a70e19a
	gopkg.in/yaml.v2 => gopkg.in/yaml.v2 v2.2.4
	k8s.io/api => k8s.io/api v0.16.4
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.16.4
	k8s.io/apimachinery => k8s.io/apimachinery v0.16.4
	k8s.io/client-go => k8s.io/client-go v0.16.4
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.16.4
	k8s.io/klog => k8s.io/klog v0.4.0
	k8s.io/kube-openapi => k8s.io/kube-openapi v0.0.0-20191107075043-30be4d16710a

	// temp while awels code isn't merged
	kubevirt.io/client-go => github.com/awels/kubevirt/staging/src/kubevirt.io/client-go v0.0.0-20201027160658-f19710e9cf38

	kubevirt.io/containerized-data-importer => kubevirt.io/containerized-data-importer v1.25.0
	sigs.k8s.io/structured-merge-diff => sigs.k8s.io/structured-merge-diff v0.0.0-20190302045857-e85c7b244fd2

)
