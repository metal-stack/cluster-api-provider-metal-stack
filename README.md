# cluster-api-provider-metal-stack

The Cluster API provider for metal-stack (CAPMS) implements the declarative management of Kubernetes cluster infrastructure.

> [!CAUTION]
> This project is currently under heavy development and is not advised to be used in production any time soon.
> Please use our stack on top of [Gardener](https://docs.metal-stack.io/stable/installation/deployment/#Gardener-with-metal-stack) instead.
> User documentation will follow as soon. Until then, head to our [CONTRIBUTING.md](/CONTRIBUTING.md).

Currently, we provide the following custom resources:

- [`MetalStackCluster`](./api/v1alpha1/metalstackcluster_types.go) can be used as [infrastructure cluster](https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-cluster) and ensures that there is a control plane IP for the cluster.
- [`MetalStackMachine`](./api/v1alpha1/metalstackmachine_types.go) bridges between [infrastructure machines](https://cluster-api.sigs.k8s.io/developer/providers/contracts/infra-machine) and metal-stack machines.

> [!note]
> Currently our infrastructure provider is only tested against the [Cluster API bootstrap provider Kubeadm (CABPK)](https://cluster-api.sigs.k8s.io/tasks/bootstrap/kubeadm-bootstrap/index.html?highlight=kubeadm#cluster-api-bootstrap-provider-kubeadm).
> While other providers might work, there is no guarantee nor the goal to reach compatibility.

## Getting started

**Prerequisites:**

- Running metal-stack installation. See our [installation](https://docs.metal-stack.io/stable/installation/deployment/) section on how to get started with metal-stack. 
- Management cluster (with network access to the metal-stack infrastructure).

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
export EXP_KUBEADM_BOOTSTRAP_FORMAT_IGNITION=true

# initialize the management cluster
clusterctl init --infrastructure metal-stack
```

> [!CAUTION]
> **Manual steps needed:**
> Due to the early development stage, manual actions are needed for the cluster to operate. Some metal-stack resources need to be created manually.

A node network needs to be created.
```bash
metalctl network allocate --description "<description>" --name <name> --project <project-id> --partition <partition>
```

A firewall needs to be created with appropriate firewall rules. An example can be found at [firewall-fules.yaml](capi-lab/firewall-rules.yaml).
```bash
metalctl firewall create --description <description> --name <name> --hostname <hostname> --project <project-id> --partition <partition> --image <image> --size <size> --firewall-rules-file=<rules.yaml> --networks internet,$(shell metalctl network list --name <name> -o template --template '{{ .id }}')
```

For your first cluster, it is advised to start with our generated template.

```bash
# display required environment variables
clusterctl generate cluster <cluster-name> --infrastructure metal-stack --list-variables

# set environment variables
export METAL_NODE_NETWORK_ID=<network-id>
export METAL_PARTITION=<partition-id>
export METAL_PROJECT_ID=<project-id>
# ...

# generate manifest
clusterctl generate cluster <cluster-name> --kubernetes-version v1.30.6 --infrastructure metal-stack
```

Apply the generated manifest from the `clusterctl` output.

```bash
kubectl apply -f <manifest>
```

Once your control plane and worker machines have been provisioned, you need to install your CNI of choice into your created cluster. This is required due to CAPI. An example is provided below: 

```bash
# get the kubeconfig
clusterctl get kubeconfig metal-test > capms-cluster.kubeconfig
# install the operator
kubectl --kubeconfig=capms-cluster.kubeconfig create -f https://raw.githubusercontent.com/projectcalico/calico/v3.28.2/manifests/tigera-operator.yaml
# install the CNI
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

Additionally, the `metal-ccm` has to be deployed for the machines to reach `Running` phase. For this use the [template](capi-lab/metal-ccm.yaml) and fill in the required variables.

If you want to provide service's of type `LoadBalancer` through MetalLB by the `metal-ccm`, you need to deploy MetalLB:

```bash
kubectl --kubeconfig capms-cluster.kubeconfig apply --kustomize capi-lab/metallb
```

For each worker node in your Kubernetes cluster, you need to create a BGP peer configuration. Replace the placeholders ({{
NODE_ASN }}, {{ NODE_HOSTNAME }}, and {{ NODE_ROUTER_ID }}) with the appropriate values for each node.

```bash
cat <<EOF | kubectl --kubeconfig=capms-cluster.kubeconfig create -f -
apiVersion: metallb.io/v1beta2
kind: BGPPeer
metadata:
  name: ${NODE_HOSTNAME}
  namespace: metallb-system
spec:
  holdTime: 1m30s
  keepaliveTime: 0s
  myASN: ${NODE_ASN}
  nodeSelectors:
  - matchExpressions:
    - key: kubernetes.io/hostname
      operator: In
      values:
      - ${NODE_HOSTNAME}
  passwordSecret: {}
  peerASN: ${NODE_ASN}
  peerAddress: ${NODE_ROUTER_ID}
EOF
```