# Development

## Getting Started Locally

This project comes with a preconfigured version of the [mini-lab](https://github.com/metal-stack/mini-lab) in [capi-lab](./capi-lab) which runs a local metal-stack instance and all prerequisites required by this provider.

```bash
make -C capi-lab

# allows access using metalctl and kubectl
eval $(make -C capi-lab --silent dev-env)
```

Next install our CAPMS provider into the cluster.

```bash
# repeat this whenever you make changes
make push-to-capi-lab
```

Before creating a cluster the control plane IP needs to be created first:

```bash
make -C capi-lab control-plane-ip
```

A basic cluster configuration that relies on `config/clusterctl-templates/cluster-template-calico.yaml` and uses the aforementioned IP can be generated and applied to the management cluster using a make target.

```bash
make -C capi-lab apply-sample-cluster
```

Once the control plane node has phoned home, run:

```bash
make -C capi-lab mtu-fix
```

When the control plane node was provisioned, you can obtain the kubeconfig like:

```bash
kubectl get secret metal-test-kubeconfig -o jsonpath='{.data.value}' | base64 -d > capi-lab/.capms-cluster-kubeconfig.yaml
# alternatively:
clusterctl get kubeconfig metal-test > capi-lab/.capms-cluster-kubeconfig.yaml
```

The node's provider ID is provided by the [metal-ccm](https://github.com/metal-stack/metal-ccm), which needs to be deployed into the cluster:


If you want to provide service's of type load balancer through MetalLB by the metal-ccm, you need to deploy MetalLB:

```bash
kubectl --kubeconfig capi-lab/.capms-cluster-kubeconfig.yaml apply --kustomize capi-lab/metallb
```

That's it!

## Running E2E Tests

Before being able to run the E2E or integration tests, make sure to set the following variables to the correct values:

```bash
export E2E_METAL_API_URL=
export E2E_METAL_API_HMAC=
export E2E_METAL_API_HMAC_AUTH_TYPE=
export E2E_METAL_PROJECT_ID=
export E2E_METAL_PROJECT_NAME=
export E2E_METAL_PARTITION=
export E2E_METAL_PUBLIC_NETWORK=
export E2E_CONTROL_PLANE_MACHINE_SIZE=
export E2E_CONTROL_PLANE_MACHINE_IMAGE_PREFIX=
export E2E_WORKER_MACHINE_SIZE=
export E2E_WORKER_MACHINE_IMAGE_PREFIX=
export E2E_FIREWALL_SIZE=
export E2E_FIREWALL_MACHINE_IMAGE=
export E2E_FIREWALL_NETWORKS=
export KUBERNETES_VERSION_UPGRADE_FROM=
export KUBERNETES_VERSION_UPGRADE_TO=
export KUBERNETES_IMAGE_UPGRADE_TO=
export E2E_KUBERNETES_VERSIONS=
```

If you want to test the local changes you made to the provider, run:

```bash
unset E2E_KUBECONFIG # ensure a new kind cluster is created
make docker-build-e2e test-e2e
```

This will automatically build and load your image.

To run the tests with a specific version, run:

```bash
export E2E_PROVIDER_VERSION=v0.7.0
export E2E_PROVIDER_CONTRACT=v1beta1
make test-e2e
```

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/cluster-api-provider-metal-stack:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/cluster-api-provider-metal-stack:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin privileges or be logged in as admin.

**Create instances of your solution**
You can apply the sample cluster configuration:

```sh
make -C capi-lab apply-sample-cluster
```

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
make -C capi-lab delete-sample-cluster
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following are the steps to build the installer and distribute this project to users.

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/cluster-api-provider-metal-stack:tag
```

NOTE: The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without
its dependencies.

2. Using the installer

Users can just run kubectl apply -f <URL for YAML BUNDLE> to install the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/cluster-api-provider-metal-stack/<tag or branch>/dist/install.yaml
```

## Quick opinionated Cluster Bootstrap and move

This is a short and opinionated fast track to create and move a cluster using our provider.
In contrast to a guide and the README, we do not explain all commands and try to be concise.

Configure your clusterctl:

```yaml
# ~/.config/cluster-api/clusterctl.yaml
providers:
  - name: "metal-stack"
    url: "https://github.com/metal-stack/cluster-api-provider-metal-stack/releases/latest/download/infrastructure-components.yaml"
    # or for PRs
    # url: "${HOME}/path/to/infrastructure-metal-stack/v0.4.0/infrastructure-components.yaml"
    # generate with:
    # IMG_TAG=branch-name RELEASE_DIR=${HOME}/path/to/infrastructure-metal-stack/v0.4.0 make release-manifests
    type: InfrastructureProvider
```

Set environment variables. Don't forget to update them along the way.

```bash
export EXP_KUBEADM_BOOTSTRAP_FORMAT_IGNITION=true

export METAL_API_HMAC=
export METAL_API_HMAC_AUTH_TYPE=
export METAL_API_URL=

export METAL_PARTITION=
export METAL_PROJECT_ID=
export CONTROL_PLANE_IP=

export FIREWALL_MACHINE_IMAGE=
export FIREWALL_MACHINE_SIZE=

export CONTROL_PLANE_MACHINE_IMAGE=
export CONTROL_PLANE_MACHINE_SIZE=
export WORKER_MACHINE_IMAGE=
export WORKER_MACHINE_SIZE=

export CLUSTER_NAME=
export NAMESPACE=default
export KUBERNETES_VERSION=v1.32.9

export CONTROL_PLANE_MACHINE_COUNT=1
export WORKER_MACHINE_COUNT=1

# Additional envs
export repo_path=$HOME/path/to/cluster-api-provider-metal-stack
export project_name=
export tenant_name=
```

Create project and control plane ip if needed:

```bash
metalctl project create --name $project_name --tenant $tenant_name --description "Cluster API test project"
metalctl network ip create --network internet --project $METAL_PROJECT_ID --name "$CLUSTER_NAME-vip" --type static -o template --template "{{ .ipaddress }}"
```

```bash
kind create cluster --name bootstrap
kind export kubeconfig --name bootstrap --kubeconfig kind-bootstrap.kubeconfig

clusterctl init --infrastructure metal-stack --kubeconfig kind-bootstrap.kubeconfig
clusterctl generate cluster $CLUSTER_NAME --infrastructure metal-stack > cluster-$CLUSTER_NAME.yaml
kubectl apply -n $NAMESPACE -f cluster-$CLUSTER_NAME.yaml

kubectl --kubeconfig kind-bootstrap.kubeconfig -n $NAMESPACE get metalstackmachines.infrastructure.cluster.x-k8s.io
export control_plane_machine_id=
metalctl machine console --ipmi $control_plane_machine_id
# ip r
# sudo systemctl restart kubeadm
# crictl ps
# ~.

clusterctl get kubeconfig > capms-cluster.kubeconfig

# metal-ccm
cat $repo_path/config/target-cluster/metal-ccm.yaml | envsubst | kubectl --kubeconfig capms-cluster.kubeconfig apply -f -

# cni
kubectl --kubeconfig=capms-cluster.kubeconfig create -f https://raw.githubusercontent.com/projectcalico/calico/v3.28.2/manifests/tigera-operator.yaml
cat <<EOF | kubectl --kubeconfig=capms-cluster.kubeconfig create -f -
apiVersion: operator.tigera.io/v1
kind: Installation
metadata:
  name: default
spec:
  # Configures Calico networking.
  calicoNetwork:
    bgp: Disabled
    ipPools:
    - name: default-ipv4-ippool
      blockSize: 26
      cidr: 10.240.0.0/12
      encapsulation: None
    mtu: 1440
  cni:
    ipam:
      type: HostLocal
    type: Calico
EOF

watch kubectl -n $NAMESPACE --kubeconfig kind-bootstrap.kubeconfig get cluster,metalstackcluster,machine,metalstackmachine,kubeadmcontrolplanes,kubeadmconfigs
# until everything is ready
```

> [!note]
> Actually, Calico should be configured using BGP (no overlay), eBPF and DSR. An example will be proposed in this repository at a later point in time.

Now you are able to move the cluster resources as you wish:

```bash
clusterctl init --infrastructure metal-stack --kubeconfig capms-cluster.kubeconfig

clusterctl move -n $NAMESPACE --kubeconfig kind-bootstrap.kubeconfig --to-kubeconfig capms-cluster.kubeconfig
# everything as expected
kubectl --kubeconfig -n $NAMESPACE kind-bootstrap.kubeconfig get cluster,metalstackcluster,machine,metalstackmachine,kubeadmcontrolplanes,kubeadmconfigs
kubectl --kubeconfig -n $NAMESPACE capms-cluster.kubeconfig get cluster,metalstackcluster,machine,metalstackmachine,kubeadmcontrolplanes,kubeadmconfigs
```
