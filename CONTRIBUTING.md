# Contributing to CAPMS

Please check out the [contributing section](https://docs.metal-stack.io/stable/development/contributing/) in our [docs](https://docs.metal-stack.io/).

## Getting Started

### Local Development

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

Before creating a cluster some manual steps are required beforehand: you need to allocate a node network and a firewall.

```bash
make -C capi-lab node-network firewall
```

A basic cluster configuration that relies on `config/clusterctl-templates/cluster-template.yaml` and uses the aforementioned node network can be generated and applied to the management cluster using a make target.

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
```

It is now expected to deploy a CNI to the cluster:

```bash
kubectl --kubeconfig=capi-lab/.capms-cluster-kubeconfig.yaml create -f https://raw.githubusercontent.com/projectcalico/calico/v3.28.2/manifests/tigera-operator.yaml
cat <<EOF | kubectl --kubeconfig=capi-lab/.capms-cluster-kubeconfig.yaml create -f -
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

> [!note]
> Actually, Calico should be configured using BGP (no overlay), eBPF and DSR. An example will be proposed in this repository at a later point in time.

The node's provider ID is provided by the [metal-ccm](https://github.com/metal-stack/metal-ccm), which needs to be deployed into the cluster:

```bash
make -C capi-lab deploy-metal-ccm
```

If you want to provide service's of type load balancer through MetalLB by the metal-ccm, you need to deploy MetalLB:

```bash
kubectl --kubeconfig capi-lab/.capms-cluster-kubeconfig.yaml apply --kustomize capi-lab/metallb
```

For each node in your Kubernetes cluster, you need to create a BGP peer configuration. Replace the placeholders ({{
NODE_ASN }}, {{ NODE_HOSTNAME }}, and {{ NODE_ROUTER_ID }}) with the appropriate values for each node.

```bash
cat <<EOF | kubectl --kubeconfig=capi-lab/.capms-cluster-kubeconfig.yaml create -f -
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

That's it!

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
