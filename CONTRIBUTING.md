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

A basic cluster configuration resides in `config/samples`.

```bash
kubectl apply -k config/samples
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

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
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
