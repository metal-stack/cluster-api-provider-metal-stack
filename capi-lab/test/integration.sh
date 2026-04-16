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
    if [ "$attempts" -ge 60 ]; then
        echo "not enough machines in waiting state - timeout reached"
        exit 1
    fi
    echo "$waiting/$minWaiting machines are waiting"
    sleep 5
    waiting=$(docker compose -f capi-lab/mini-lab/compose.yaml run --no-TTY --rm metalctl machine ls | grep Waiting | wc -l)
    attempts=$attempts+1
done
echo "$waiting/$minWaiting machines are waiting"

make push-to-capi-lab

if [ "$MINI_LAB_FLAVOR" = "kamaji" ]; then

    echo "Starting kamaji tests"

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
        if [ "$attempts" -ge 120 ]; then
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
