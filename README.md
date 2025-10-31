# cluster-api-provider-metal-stack

The Cluster API provider for metal-stack (CAPMS) implements the declarative management of Kubernetes cluster infrastructure on top of [metal-stack](https://metal-stack.io/) using [Cluster API (CAPI)](https://cluster-api.sigs.k8s.io/).

> [!WARNING]
> As of now the CAPMS is not yet feature complete and there might be breaking changes in future releases.
> In case you search for a feature stable alternative consider [Gardener on metal-stack](https://docs.metal-stack.io/stable/installation/deployment/#Gardener-with-metal-stack) instead.
> For developing this project head to our [DEVELOPMENT.md](/DEVELOPMENT.md).

Currently, we provide the following custom resources:

- [`MetalStackCluster`](./api/v1alpha1/metalstackcluster_types.go) can be used as [infrastructure cluster](https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-cluster) and ensures that there is a control plane IP for the cluster.
- [`MetalStackMachine`](./api/v1alpha1/metalstackmachine_types.go) bridges between [infrastructure machines](https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-machine) and metal-stack machines.

We plan to cover more resources in the future:

- Node Networks
- Firewall Deployments
- Improved configuration suggestion of CNIs

> [!note]
> Currently our infrastructure provider is only tested against the [Cluster API bootstrap provider Kubeadm (CABPK)](https://cluster-api.sigs.k8s.io/tasks/bootstrap/kubeadm-bootstrap/index.html?highlight=kubeadm#cluster-api-bootstrap-provider-kubeadm).
> While other providers might work, there is no guarantee nor the goal to reach compatibility.

## Getting started

**Prerequisites:**

- Running metal-stack installation. See our [installation](https://docs.metal-stack.io/stable/installation/deployment/) section on how to get started with metal-stack.
- Operating system images available to metal-stack. See [metal-stack/metal-images](https://github.com/metal-stack/metal-images) for pre-built ones.
- Management cluster (with network access to the metal-stack infrastructure).
- CLI metalctl installed for communicating with the metal-api. Installation instructions can be found in the corresponding [repository](https://github.com/metal-stack/metalctl).
- CLI clusterctl

First, add the metal-stack infrastructure provider to your `clusterctl.yaml`:

```yaml
# ~/.config/cluster-api/clusterctl.yaml
providers:
  - name: "metal-stack"
    url: "https://github.com/metal-stack/cluster-api-provider-metal-stack/releases/latest/download/infrastructure-components.yaml"
    type: InfrastructureProvider
```

Now, you are able to install the CAPMS into your management cluster:

```bash
# export the following environment variables
export METAL_API_URL=<url>
export METAL_API_HMAC=<hmac>
export METAL_API_HMAC_AUTH_TYPE=<Metal-Admin or Metal-Edit>
export EXP_KUBEADM_BOOTSTRAP_FORMAT_IGNITION=true

# initialize the management cluster
clusterctl init --infrastructure metal-stack
```

> [!CAUTION]
> **Manual steps needed:**
> Due to the early development stage, manual actions are needed for the cluster to operate. Some metal-stack resources need to be created manually.

A node network needs to be created.
```bash
export CLUSTER_NAME=<cluster-name>
export METAL_PARTITION=<partition>
export METAL_PROJECT_ID=<project-id>
metalctl network allocate --description "Node network for $CLUSTER_NAME" --name $CLUSTER_NAME --project $METAL_PROJECT_ID --partition $METAL_PARTITION

# export environment variable for use in the next steps
export METAL_NODE_NETWORK_ID=$(metalctl network list --name $CLUSTER_NAME -o template --template '{{ .id }}')
```

Allocate a VIP for the control plane.

```bash
export CONTROL_PLANE_IP=$(metalctl network ip create --network internet --project $METAL_PROJECT_ID --name "$CLUSTER_NAME-vip" --type static -o template --template "{{ .ipaddress }}")
```

A firewall needs to be created with appropriate firewall rules. An example can be found at [firewall-rules.yaml](config/target-cluster/firewall-rules.yaml).
```bash
# export environment variable for the firewall image and size
export FIREWALL_MACHINE_IMAGE=<firewall-image>
export FIREWALL_MACHINE_SIZE=<machine-size>

metalctl firewall create --description "Firewall for $CLUSTER_NAME" --name "$CLUSTER_NAME-fw" --hostname "$CLUSTER_NAME-fw" --project $METAL_PROJECT_ID --partition $METAL_PARTITION --image $FIREWALL_MACHINE_IMAGE  --size $FIREWALL_MACHINE_SIZE --firewall-rules-file=<rules.yaml> --networks internet,$METAL_NODE_NETWORK_ID
```

For your first cluster, it is advised to start with our generated template. Ensure that the namespaced cluster name is unique within the metal stack project.

```bash
# display required environment variables
clusterctl generate cluster $CLUSTER_NAME --infrastructure metal-stack --list-variables

# set additional environment variables
export CONTROL_PLANE_MACHINE_IMAGE=<machine-image>
export CONTROL_PLANE_MACHINE_SIZE=<machine-size>
export WORKER_MACHINE_IMAGE=<machine-image>
export WORKER_MACHINE_SIZE=<machine-size>

# generate manifest
clusterctl generate cluster $CLUSTER_NAME --kubernetes-version v1.32.9 --infrastructure metal-stack
```

Apply the generated manifest from the `clusterctl` output.

```bash
kubectl apply -f <manifest>
```

Once your control plane and worker machines have been provisioned, you need to install your CNI of choice into your created cluster. This is required due to CAPI. An example is provided below:

```bash
# get the kubeconfig
clusterctl get kubeconfig metal-test > capms-cluster.kubeconfig

# install the calico operator
kubectl --kubeconfig=capms-cluster.kubeconfig create -f https://raw.githubusercontent.com/projectcalico/calico/v3.28.2/manifests/tigera-operator.yaml

# install the calico CNI
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
```

Meanwhile, the `metal-ccm` has to be deployed for the machines to reach `Running` phase. For this use the [`config/target-cluster/metal-ccm.yaml` template](config/target-cluster/metal-ccm.yaml) and fill in the required variables.

```bash
export NAMESPACE=<namespace>
export CLUSTER_NAME=<cluster name>
cat config/target-cluster/metal-ccm.yaml | envsubst | kubectl --kubeconfig capms-cluster.kubeconfig apply -f -
```

If you want to provide service's of type `LoadBalancer` through MetalLB by the `metal-ccm`, you need to deploy MetalLB:

```bash
kubectl --kubeconfig capms-cluster.kubeconfig apply --kustomize capi-lab/metallb
```

## Frequently Asked Questions

### I need to know the Control Plane IP address in advance. Can I provide a static IP address in advance?

Yes, simply create a static IP address and set it to `metalstackcluster/$CLUSTER_NAME.spec.controlPlaneIP`.

```bash
metalctl network ip create --name $CLUSTER_NAME-vip --project $METAL_PROJECT_ID --type static
```

### I'd like to have a specific Pod CIDR. How can I achieve this?

When generating your cluster, set `POD_CIDR` to your desired value.

```bash
export POD_CIDR=["10.240.0.0/12"]
```

## Flavors

You might choose from different cluster template [flavors](https://cluster-api.sigs.k8s.io/clusterctl/commands/generate-cluster.html?highlight=flavor#flavors) to generate manifests with clusterctl. Here is a table describing the available flavors:

| Name      | Description                                                                                                                                                                                                                                                                            | K8s Compatibility |
| --------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------- |
|           | This is the default flavor without providing the `--flavor` flag. This expects the user to deploy a CNI and a CCM.                                                                                                                                                                     | >= v1.33          |
| calico    | Installs [calico](https://docs.tigera.io/calico/latest/about/) CNI along with [metal-ccm](https://github.com/metal-stack/metal-ccm). Depends on `ClusterResourceSet` and the [Add-on Provider for Helm](https://github.com/kubernetes-sigs/cluster-api-addon-provider-helm/tree/main). | >= v1.33          |
| pre-v1.33 | The same as the default flavor but working for K8s versions < v1.33.                                                                                                                                                                                                                   | < v1.33           |
