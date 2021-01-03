# CSI KubeVirt Driver

This repository hosts the CSI KubeVirt driver and all of its build and dependent configuration files to deploy the driver.

## Pre-requisite
- Kubernetes cluster
- Running version 1.18 or later
- Access to terminal with `kubectl` installed

## Deployment
//TODO WIP
- use `deploy/infra-cluster-service-account.yaml` to create a service account in kubevirt cluster (use '-n' flag in create command for specifying the kubevirt cluster namepsace)
- create kubeconfig for service account
    - Use `deploy/example/infracluster-kubeconfig.yaml` as a reference. Inside the file there are instructions for fields that need to be edited.
    - Test your kubeconfig. Try listing resources of type VMI in the kubevirt cluster namepsace.
- create namespace for the driver in tenant cluster
    - Use `deploy/000-namespace.yaml`
- use `deploy/secret.yaml` for creating the necessary secret in the tenant cluster
    - set kubeconfig: [base64 of kubeconfig from previous step]
- use `deploy/configmap.yaml` for creating the driver's config
    - set infraClusterNamespace to the kubevirt cluster namepsace.
- deploy files under `deploy` in  tenant cluster
    - 000-csi-driver.yaml
    - 020-authorization.yaml
    - 030-node.yaml
    - 040-controller.yaml
- create StorageClass and PersistentVolumeClaim - see `deploy/example`
- Enable HotplugVolumes feature gate
    - In case your Kubevirt namespace has the ConfigMap 'kubevirt-config' then use `deploy/example/kubevirt-config.yaml` for adding the feature gate to it. Look at the path {.data.feature-gates}
    - Otherwise, add the feature gate to the resource of type Kubevirt. There should be a single resource of this type and its name is irrelevant. See `deploy/example/kubevirt.yaml`
    - Pay attention that in some deployments there are operators that will restore previous configuration. You will have to stop these operators for editing the resources. E.g. hco-operator in HCO.

## Examples

## Building the binaries

If you want to build the driver yourself, you can do so with the following command from the root directory:

```shell
make build
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
