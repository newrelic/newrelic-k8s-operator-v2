<a href="https://opensource.newrelic.com/oss-category/#community-project"><picture><source media="(prefers-color-scheme: dark)" srcset="https://github.com/newrelic/opensource-website/raw/main/src/images/categories/dark/Community_Project.png"><source media="(prefers-color-scheme: light)" srcset="https://github.com/newrelic/opensource-website/raw/main/src/images/categories/Community_Project.png"><img alt="New Relic Open Source community project banner." src="https://github.com/newrelic/opensource-website/raw/main/src/images/categories/Community_Project.png"></picture></a>  


# New Relic K8s Resource Operator
- [Overview](#overview)
- [Quick Start](#quick-start)
- [Provisioning Resources](#provisioning-resources)
- [Development](#development)


# Overview
Kubernetes operator that facilitates management of New Relic resources from within your K8s configuration. Currently supports:

- Alert Policies
- NRQL Alert Conditions
- Alert Destinations
- Alert Channels
- Alert Workflows

If you are looking for New Relic's Kubernetes operator for managing New Relic's Kubernetes integration, please see [newrelic-k8s-operator](https://github.com/newrelic/newrelic-k8s-operator).

# Quick Start

## Running Locally w/ Kind

1. Install docker, kubectl, kustomize, and kind

```bash
brew cask install docker
brew install kubernetes-cli kustomize kind
```

2. Create a test cluster with `kind`

```bash
kind create cluster --name newrelic
kubectl cluster-info
```

3. Clone the repo, build the docker image locally (using dev tag as an example)

```bash
git clone git@github.com:newrelic/newrelic-k8s-operator-v2.git
cd newrelic-k8s-operator-v2
docker build -t newrelic/newrelic-k8s-operator-v2:dev .
```

4. Load the image into the kind cluster

```bash
 kind load docker-image newrelic/newrelic-k8s-operator-v2:dev --name newrelic-test
```
> <small>**Note:** Kind's default image pull policy for `:latest` tags is `Always`, so it's recommended to use a tag other than `latest`. For more information see [Kind's docs](https://kind.sigs.k8s.io/docs/user/quick-start/#loading-an-image-into-your-cluster)</small>


5. Install the operator with provided dev `kustomization.yaml` and confirm installation

```bash
kustomize build config/dev | kubectl apply -f -
```
> <small>**Note:** This will install operator on whatever kubernetes cluster kubectl is configured to use.</small>

6. Validate pods are running

```bash
kubectl get pods -n newrelic-k8s-operator-v2-system
```

## Using a custom container

Alternatively, you can deploy the operator in a custom container by overriding the image name in a `kustomization.yaml`file:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: newrelic-k8s-operator-v2-system
resources:
  - github.com/newrelic/newrelic-k8s-operator-v2/config/default
images:
- name: newrelic/newrelic-k8s-operator-v2:latest
  newName: <CUSTOM_IMAGE>
  newTag: <CUSTOM_TAG>
```

Then apply the file with:

```bash
kustomize build . | kubectl apply -f -
```

## Uninstall

The operator can be removed with the reverse of installation:

```bash
kustomize build config/dev | kubectl delete -f -
```


..or if installed without cloning repo:

```bash
kustomize build github.com/newrelic/newrelic-k8s-operator-v2/config/default | kubectl delete -f -
```

# Provisioning Resources

Once the operator is successfully deployed to a cluster, resources can be provisioned with NR k8s objects. There are detailed examples provided under the [examples](examples/) section.

## Alert Policy Example

Using the [policy example](examples/policies/example_policy.yml) provided. Input the fields within the configuration, including a [user API key](https://docs.newrelic.com/docs/apis/intro-apis/new-relic-api-keys/#user-key), accountId to create the resource within, and policy specific inputs.

> <small>**Note:** You can also use a [Kubernetes secret](https://kubernetes.io/docs/concepts/configuration/secret/) for providing your API key. We've provided an [example secret](/examples/example_secret.yml) configuration file in case you want to use this method. You'll need to replace `api_key` with [`api_key_secret`](examples/policies/example_policy.yml#L11). </small>


```yaml
apiVersion: alerts.k8s.newrelic.com/v1
kind: AlertPolicy
metadata:
  labels:
    app.kubernetes.io/name: newrelic-kubernetes-operator-v2
    app.kubernetes.io/managed-by: kustomize
  name: alertpolicy-example
spec:
  apiKey: <api_key>
  # apiKeySecret:
  #   name: nr-api-key
  #   namespace: default
  #   keyName: api-key
  accountId: 1
  region: "US"
  name: test-policy
  incidentPreference: "PER_CONDITION"
```

The config can then be applied with:

```bash
 kubectl apply -f examples/example_policy.yaml
```

To see configured policies, run the command:

```bash
kubectl describe alertpolicies.alerts.k8s.newrelic.com
```

The operator will then create and update this policy within your New Relic account as needed by applying changes with `kubectl apply -f <filename>`

This process can be repeated for any of the examples provided. For more detail on all inputs - see Terraform docs for corresponding resources, which contain the same input definitions as this operator:

- [Alert Policies](https://registry.terraform.io/providers/newrelic/newrelic/latest/docs/resources/alert_policy)
- [NRQL Conditions](https://registry.terraform.io/providers/newrelic/newrelic/latest/docs/resources/nrql_alert_condition)
- [Alert Destinations](https://registry.terraform.io/providers/newrelic/newrelic/latest/docs/resources/notification_destination)
- [Alert Channels](https://registry.terraform.io/providers/newrelic/newrelic/latest/docs/resources/notification_channel)
- [Alert Workflows](https://registry.terraform.io/providers/newrelic/newrelic/latest/docs/resources/workflow)



# Development

## Prerequisites
- [Go](https://golang.org/) v1.22.0+
- [Docker](https://www.docker.com/get-started) (with Kubernetes enabled)
- [kubectl](https://kubernetes.io/docs/tasks/tools/install-kubectl/)
- [kustomize](https://kustomize.io/)
- [kubebuilder](https://book.kubebuilder.io/quick-start.html)

## Getting Started

1. Clone the repo

```bash
git clone git@github.com:newrelic/newrelic-k8s-operator-v2.git
```

2. Install [kubebuilder](https://go.kubebuilder.io/quick-start.html#prerequisites) following the instructions for your operating system. This installation will also get `etcd` and `kube-apiserver` which are needed for the tests. <br>
    > <small>**Note:** Do **_not_** install `kubebuilder` with `brew`. Homebrew's `kubebuilder` package will not provide all the necessary dependencies for running the tests.</small>

3. Spin up a test cluster w/ Kind:

```bash
kind create cluster --name newrelic
kubectl cluster-info
```

4. Run `make install` to install the operator on the local cluster. Confirm your configuration was deployed with:

- Show your namespaces. You should see `newrelic-k8s-operator-v2-system` in the list of namespaces.
  ```bash
  kubectl get namespaces
  ```
- Show the nodes within the `newrelic-k8s-operator-v2-system` namespace.
  ```bash
  kubectl get nodes -n newrelic-k8s-operator-v2-system
  ```
  You should see something similar to the following output:
  ```
  NAME                          STATUS   ROLES           AGE    VERSION
  newrelic-test-control-plane   Ready    control-plane   116s   v1.28.7
  ```

5. Run `make run` in a separate terminal cd'd into the repo - This allows a live run/logging of the operator.

6. Configure and apply example configurations from [provided examples](./examples)

```bash
kubectl apply -f examples/<resource>/<example_resource.yml>
```

A resource can be deleted as well with:

```bash
kubectl delete -f examples/<resource>/<example_resource.yml>
```

## Additional Commands

### Make
Below are other useful commands - See `Makefile` for full list.

```bash
# Re-Generate CRDs if any api spec changes occur, alternatively `make` be ran
make manifests

# Run Ginkgo tests
make test 

# Undeploy a resource
make undeploy

# Uninstall operator
make uninstall

# Build the manager binary under `bin/`
make build

# Build a docker image
make docker-build
```

### Kubectl

```bash
# Get the node being used for the newrelic operator.
kubectl get nodes -n newrelic-k8s-operator-v2-system

# Describe the node being used for the newrelic operator.
kubectl describe node <your-node-name>

# Tail logs of the operator's manager container (useful during development).
# Use the `describe node` command above to locate your manager controller.
kubectl logs -f -n newrelic-k8s-operator-v2-system -c manager newrelic-kubernetes-operator-controller-manager-<hash from>
```

## Contributing

We encourage your contributions to improve the *K8s New Relic Resource Operator*! Keep in mind that when you submit your pull request, you'll need to sign the CLA via the click-through using CLA-Assistant. You only have to sign the CLA one time per project.

If you have any questions, or to execute our corporate CLA (which is required if your contribution is on behalf of a company), drop us an email at opensource@newrelic.com.

**A note about vulnerabilities**

As noted in our [security policy](../../security/policy), New Relic is committed to the privacy and security of our customers and their data. We believe that providing coordinated disclosure by security researchers and engaging with the security community are important means to achieve our security goals.

If you believe you have found a security vulnerability in this project or any of New Relic's products or websites, we welcome and greatly appreciate you reporting it to New Relic through [HackerOne](https://hackerone.com/newrelic).

If you would like to contribute to this project, review [these guidelines](./CONTRIBUTING.md).

To all contributors, we thank you!  Without your contribution, this project would not be what it is today.

## License
*K8s New Relic Resource Operator* is licensed under the [Apache 2.0](http://apache.org/licenses/LICENSE-2.0.txt) License.
