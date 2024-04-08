
# CSI KubeVirt Driver

This repository hosts the CSI KubeVirt driver and all of its build and dependent configuration files to deploy the driver.

This CSI driver is made for a tenant cluster deployed on top of kubevirt VMs, and enables it to get its persistent data  
from the underlying, infrastructure cluster.
To avoid confusion, this CSI driver is deployed on the tenant cluster, and does not require kubevirt installation at all.

The term "tenant cluster" refers to the k8s cluster installed on kubevirt VMs, and "infrastructure cluster"  
(or shorter "infra cluster") refers to a cluster with kubevirt installed and can be installaed on any infrastrucure (baremetal, public cloud, etc).

![](docs/high-level-diagram.svg)

## Pre-requisite
- Kubernetes cluster
- Running version 1.24 or later
- Access to terminal with `kubectl` installed

## Deployment
For this example it is assumed that the namespace in which the tenant VMs are deployed is called `kvcluster`.

For split deployment
```bash
# Deploy infra service account
kubectl -n kvcluster apply -f ./deploy/infra-cluster-service-account.yaml
# Deploy the tenant resources not including the controller with the overlay in deploy/tenant/overlay
kubectl_tenant apply --kubeconfig $TENANT_KUBECONFIG --kustomize ./deploy/tenant/overlay
# Deploy the controller resources in the infra cluster with the overlay in deploy/controller-infra/overlay
kubectl apply --kustomize ./deploy/controller-infra/overlay
```

For tenant controller deployment
```bash
# Deploy the infra kubeconfig secret in the tenant cluster, this depends on the infra cluster.
kubectl -n kubevirt-csi-driver --kubeconfig $TENANT_KUBECONFIG apply -f <yaml of secret>
# Deploy infra service account
kubectl -n kvcluster apply -f ./deploy/infra-cluster-service-account.yaml
# Deploy the tenant resources not including the controller with the overlay in deploy/tenant/overlay
kubectl_tenant apply --kubeconfig $TENANT_KUBECONFIG --kustomize ./deploy/tenant/overlay
# Deploy the controller resources in the tenant cluster with the overlay in deploy/controller-tenant/overlay
kubectl apply --kubeconfig $TENANT_KUBECONFIG --kustomize ./deploy/controller-tenant/overlay
```
### Split deployment
A split deployment is where the controller is deployed in the namespace of the tenant cluster inside of the infra cluster. This means the controller lives in the same namespace as the tenant cluster Virtual Machines. The daemonset is still deployed in the tenant cluster. This allows to not give the tenat cluster access to the infra cluster in order to manage DataVolumes in the infra cluster.

`./deploy/controller-infra/overlay/kustomize.yaml` looks like this:
```yaml
bases:
- ../base
namespace: kvcluster
patchesStrategicMerge:
- controller.yaml
resources:
- infra-namespace-configmap.yaml
```
Note the namespace is the namespace of the tenant cluster in the infra cluster.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: driver-config
  namespace: kubevirt-csi-driver
data:
  infraClusterNamespace: kvcluster
  infraClusterLabels: csi-driver/cluster=tenant
```
For more information about the fields available in the `driver-config` ConfigMap see this [documentation](docs/snapshot-driver-config.md)
```yaml
kind: Deployment
apiVersion: apps/v1
metadata:
  name: kubevirt-csi-controller
  namespace: kubevirt-csi-driver
  labels:
    app: kubevirt-csi-driver
spec:
  template:
    spec:
      containers:
        - name: csi-driver
          image: <registry visible in tenant>/kubevirt-csi-driver:latest
```

### Full tenant deployment
A Tenant deployment is where both controller and daemonset are deployed in the tenant cluster. This means the controller in the tenant cluster needs access to the infra cluster in order to manage DataVolumes in the infra cluster. In addition to applying the controller the common tenant resources also have to be applied.

`./deploy/controller-tenant/overlay/kustomize.yaml` looks like this:
```yaml
bases:
- ../base
namespace: kubevirt-csi-driver
patchesStrategicMerge:
- controller.yaml
```
The namespace in the tenant cluster is `kubevirt-csi-driver` and we are merging the controller.yaml into the main deployment yaml.
The base deployment references a secret called `kvcluster-kubeconfig` which should contain the kubeconfig that allows the controller to manage DataVolumes in the infra cluster. It is expected this secret is created with the overlay, and specified in the `kustomize.yaml`

Deployment supports [kustomize](https://github.com/kubernetes-sigs/kustomize) when deploying the controller and you can use an overlay like this `./deploy/controller-tenant/overlay/controller.yaml`
```yaml
kind: Deployment
apiVersion: apps/v1
metadata:
  name: kubevirt-csi-controller
  namespace: kubevirt-csi-driver
  labels:
    app: kubevirt-csi-driver
spec:
  template:
    spec:
      containers:
        - name: csi-driver
          image: <registry visible in tenant>/kubevirt-csi-driver:latest
```
Replace the registry and image to the one used. 

### Common tenant resources
Both tenant deployment and split deployments require resources that are common to be deployed in the tenant cluster. The overlay references the base in `./deploy/tenant/base` like this:
```yaml
bases:
- ../base
namespace: kubevirt-csi-driver
patchesStrategicMerge:
- infra-namespace-configmap.yaml
- node.yaml
- storageclass.yaml
```
The namespace has to match the namespace in the controller-tenant deployment if you use that method of deploying.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: driver-config
  namespace: kubevirt-csi-driver
data:
  infraClusterNamespace: kvcluster #namespace in infra cluster that holds the tenant cluster VMs
  infraClusterLabels: csi-driver/cluster=tenant
```
Change the `infraClusterNamespace` to be what is in use in your cluster.

```yaml
kind: DaemonSet
apiVersion: apps/v1
metadata:
  name: kubevirt-csi-node
  namespace: kubevirt-csi-driver
spec:
  template:
    spec:
      containers:
        - name: csi-driver
          image: <registry visible in tenant>/kubevirt-csi-driver:latest
```
Replace the registry and image to the one used. 

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: kubevirt
  annotations:
    storageclass.kubernetes.io/is-default-class: "true"
provisioner: csi.kubevirt.io
parameters:
  infraStorageClassName: local
  bus: scsi
```
Set `infraStorageClassName` to the storage class in the infra cluster that will are used to create the DataVolumes in. 

### Configuring KubeVirt

Enable HotplugVolumes feature gate:
  - In case your Kubevirt namespace has the ConfigMap 'kubevirt-config' then use `deploy/example/kubevirt-config.yaml` for adding the feature gate to it. Look at the path {.data.feature-gates}
  - Otherwise, add the feature gate to the resource of type Kubevirt. There should be a single resource of this type and its name is irrelevant. See `deploy/example/kubevirt.yaml`
  - Pay attention that in some deployments there are operators that will restore previous configuration. You will have to stop these operators for editing the resources.Some operators allow configuration through their own CRD. HCO is such an operator. See [HCO cluster configuration](https://github.com/kubevirt/hyperconverged-cluster-operator/blob/master/docs/cluster-configuration.md) to understand how HCO feature gates are configured.

## Building the binaries

If you want to build the driver yourself, you can do so with the following command from the root directory:

```shell
make build
```

## Run functional tests

Running the functional tests will use an existing cluster (looks for `KUBECONFIG`) to deploy a tenant k8s cluster 
and will deploy the CSI driver on it, and a test pod that consumes a dynamically provisioned volume.

```shell
make test-functional IMG=quay.io/kubevirt/csi-driver:latest
```

You can choose to run the tests on a specific namespace. That namespace will not be terminated in the end of the run.
```shell
KUBEVIRT_CSI_DRIVER_FUNC_TEST_NAMESPACE=my-namespace make test-functional
```
## Submitting patches

When sending patches to the project, the submitter is required to certify that
they have the legal right to submit the code. This is achieved by adding a line

    Signed-off-by: Real Name <email@address.com>

to the bottom of every commit message. Existence of such a line certifies
that the submitter has complied with the Developer's Certificate of Origin 1.1,
(as defined in the file docs/developer-certificate-of-origin).

This line can be automatically added to a commit in the correct format, by
using the '-s' option to 'git commit'.

# Community

If you got enough of code and want to speak to people, then you got a couple
of options:

* Chat with us on Slack via [#virtualization @ kubernetes.slack.com](https://kubernetes.slack.com/?redir=%2Farchives%2FC8ED7RKFE)
* Discuss with us on the [kubevirt-dev Google Group](https://groups.google.com/forum/#!forum/kubevirt-dev)

### Code of conduct

[Code of conduct](CODE_OF_CONDUCT.md)

## License

KubeVirt CSI Driver is distributed under the
[Apache License, Version 2.0](http://www.apache.org/licenses/LICENSE-2.0.txt).

    Copyright 2016

    Licensed under the Apache License, Version 2.0 (the "License");
    you may not use this file except in compliance with the License.
    You may obtain a copy of the License at

        http://www.apache.org/licenses/LICENSE-2.0

    Unless required by applicable law or agreed to in writing, software
    distributed under the License is distributed on an "AS IS" BASIS,
    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
    See the License for the specific language governing permissions and
    limitations under the License.
