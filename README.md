# CSI KubeVirt Driver

This repository hosts the CSI KubeVirt driver and all of its build and dependent configuration files to deploy the driver.

## Pre-requisite
- Kubernetes cluster
- Running version 1.18 or later
- Access to terminal with `kubectl` installed

## Deployment
//TODO WIP
- create a service-account with name on the kubevirt cluster (infra-cluster)
- set RBAC rules for the service account (//TODO supply file, doc)
- designate a namespace to deploy the VMs for the tenant cluster nodes
- deploy tenant cluster
- deploy files under `deploy/*`
- edit the secret `infra-cluster-credentials` in namespace `kubevirt-csi-driver`
    - set apiUrl: [base64 of https://<infra-cluster-api-url>]  
    - set service-ca.crt : [base64 of <infra-cluster serivce-ca.crt>] (copy service-ca.crt value from infra-cluster-sa secret)
    - set namespace: [base64 of <infra-cluster namespace>]
    - set token : [base64 of <infra-cluser token>] (copy token value from infra-cluster-sa secret )
- create StorageClass and PersistentVolumeClaim - see `deploy/example`
    
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
