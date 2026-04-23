#!/usr/bin/env bash
set -e

echo "Starting capi-lab"
make -C capi-lab
eval $(make -C capi-lab --silent dev-env)

echo "Waiting for machines to get to waiting state"
waiting=$(docker compose -f capi-lab/mini-lab/compose.yaml run --no-TTY --rm metalctl machine ls | grep Waiting | wc -l)
minWaiting=2
declare -i attempts=0
until [ "$waiting" -ge $minWaiting ]
do
    if [ "$attempts" -ge 180 ]; then
        echo "not enough machines in waiting state - timeout reached"
        exit 1
    fi
    echo "$waiting/$minWaiting machines are waiting"
    sleep 5
    waiting=$(docker compose -f capi-lab/mini-lab/compose.yaml run --no-TTY --rm metalctl machine ls | grep Waiting | wc -l)
    attempts+=1
done
echo "$waiting/$minWaiting machines are waiting"

make push-to-capi-lab

if [ "$MINI_LAB_FLAVOR" = "capms_dell_sonic" ] || [ "$MINI_LAB_FLAVOR" = "capms_sonic" ]; then

    if [ "$MINI_LAB_FLAVOR" = "capms_dell_sonic" ]; then
        echo "Starting capms dell sonic flavor tests"
    else
        echo "Starting capms sonic flavor tests"
    fi

    export CLUSTER_NAME=metal-test

    echo "Creating control plane IP"
    make -C capi-lab control-plane-ip

    echo "Applying sample cluster"
    make -C capi-lab apply-sample-cluster

    echo "Waiting for firewall, control-plane and worker to get to Phoned Home state"
    phoned=$(docker compose -f capi-lab/mini-lab/compose.yaml run --no-TTY --rm metalctl machine ls | grep Phoned | wc -l)
    minPhoned=3
    declare -i attempts=0
    until [ "$phoned" -ge $minPhoned ]
    do
        if [ "$attempts" -ge 180 ]; then
            echo "not enough machines phoned home - timeout reached"
            exit 1
        fi
        echo "$phoned/$minPhoned machines have phoned home"
        sleep 5
        phoned=$(docker compose -f capi-lab/mini-lab/compose.yaml run --no-TTY --rm metalctl machine ls | grep Phoned | wc -l)
        attempts+=1
    done
    echo "$phoned/$minPhoned machines have phoned home"

    echo "Applying mtu fix"
    make -C capi-lab mtu-fix

    echo "Waiting for cluster to be provisioned"
    declare -i attempts=0
    until kubectl --kubeconfig ${KUBECONFIG} get cluster ${CLUSTER_NAME} -o jsonpath='{.status.phase}' 2>/dev/null | grep -q "Provisioned"
    do
        if [ "$attempts" -ge 180 ]; then
            echo "cluster was not provisioned - timeout reached"
            kubectl --kubeconfig ${KUBECONFIG} get cluster ${CLUSTER_NAME} -o yaml || true
            exit 1
        fi
        echo "cluster ${CLUSTER_NAME} is not yet provisioned"
        sleep 5
        attempts+=1
    done
    echo "Cluster ${CLUSTER_NAME} is provisioned"

    echo "Generating kubeconfig for sample cluster"
    make -C capi-lab sample-cluster-kubeconfig

    echo "Waiting for tenant API server to be reachable"
    declare -i attempts=0
    until kubectl --kubeconfig ${CLUSTER_NAME}.kubeconfig version >/dev/null 2>&1
    do
        if [ "$attempts" -ge 180 ]; then
            echo "tenant API server not reachable - timeout reached"
            kubectl --kubeconfig ${CLUSTER_NAME}.kubeconfig version || true
            exit 1
        fi
        echo "tenant API server not reachable yet"
        sleep 5
        attempts+=1
    done
    echo "Tenant API server is reachable"

    echo "Deploying metal-ccm to sample cluster"
    make -C capi-lab sample-cluster-deploy-metal-ccm

    echo "Waiting for control-plane and worker node to become Ready"
    minReady=2
    ready=0
    declare -i attempts=0
    until [ "$ready" -ge $minReady ]
    do
        if [ "$attempts" -ge 180 ]; then
            echo "not enough nodes became Ready - timeout reached"
            kubectl --kubeconfig ${CLUSTER_NAME}.kubeconfig get nodes || true
            exit 1
        fi
        echo "$ready/$minReady nodes are Ready"
        sleep 5
        ready=$(kubectl --kubeconfig ${CLUSTER_NAME}.kubeconfig get nodes --no-headers 2>/dev/null | awk '{ print $2 }' | grep -c "^Ready$" || true)
        attempts+=1
    done
    echo "$ready/$minReady nodes are Ready"
    kubectl --kubeconfig ${CLUSTER_NAME}.kubeconfig get nodes

fi


if [ "$MINI_LAB_FLAVOR" = "kamaji" ]; then

    echo "Starting kamaji flavor tests"

    echo "Creating control plane IP"
    export CLUSTER_NAME=kamaji-tenant-test
    make -C capi-lab control-plane-ip

    echo "Creating kamaji tenant"
    export TENANT_NAMESPACE=kamaji-tenant-test
    make -C capi-lab create-kamaji-tenant

    echo "Waiting for machines to get to Phoned Home state"
    phoned=$(docker compose -f capi-lab/mini-lab/compose.yaml run --no-TTY --rm metalctl machine ls | grep Phoned | wc -l)
    minPhoned=2
    declare -i attempts=0
    until [ "$phoned" -ge $minPhoned ]
    do
        if [ "$attempts" -ge 180 ]; then
            echo "not enough machines phoned home - timeout reached"
            exit 1
        fi
        echo "$phoned/$minPhoned machines have phoned home"
        sleep 5
        phoned=$(docker compose -f capi-lab/mini-lab/compose.yaml run --no-TTY --rm metalctl machine ls | grep Phoned | wc -l)
        attempts+=1
    done
    echo "$phoned/$minPhoned machines have phoned home"

    echo "Checking if cluster was created"
    if kubectl get cluster -n ${TENANT_NAMESPACE} | grep -e "kamaji-tenant-test" -e "Provisioned"; then
        echo "Cluster resource exists and is provisioned"
    else
        echo "Cluster resource does not exist"
        exit 1
    fi


    echo "Generating kubeconfig for tenant cluster"
    make -C capi-lab kamaji-tenant-kubeconfig

    echo "Checking if tenant cluster exists"
    if kubectl --kubeconfig ${CLUSTER_NAME}.kubeconfig get nodes | grep -e "Ready"; then
        echo "Nodes have joined the cluster and are ready"
    elif kubectl --kubeconfig ${CLUSTER_NAME}.kubeconfig get nodes | grep -e "No resources found"; then
        echo "Nodes have not joined yet"
        # TODO network issues can't be fixed by this test,
        # so we shouldn't fail the test if the nodes haven't joined yet
        # exit 1
    fi

fi

echo "Successfully started capi-lab"
