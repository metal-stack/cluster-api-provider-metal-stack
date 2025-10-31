package frmwrk

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	. "github.com/onsi/gomega"    //nolint:staticcheck

	metalfw "github.com/metal-stack/metal-go/api/client/firewall"
	metalip "github.com/metal-stack/metal-go/api/client/ip"
	metalmachine "github.com/metal-stack/metal-go/api/client/machine"
	metalnetwork "github.com/metal-stack/metal-go/api/client/network"
	metalmodels "github.com/metal-stack/metal-go/api/models"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	kubeadmvbootstrap1 "sigs.k8s.io/cluster-api/bootstrap/kubeadm/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"

	capmsv1alpha1 "github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
)

type ClusterConfig struct {
	SpecName          string
	NamespaceName     string
	ClusterName       string
	KubernetesVersion string
	ControlPlaneIP    string

	FirewallSize     string
	FirewallImage    string
	FirewallNetworks []string

	ControlPlaneMachineCount int64
	ControlPlaneMachineSize  string
	ControlPlaneMachineImage string

	WorkerMachineCount int64
	WorkerMachineSize  string
	WorkerMachineImage string
}

type E2ECluster struct {
	E2EContext *E2EContext
	ClusterConfig

	Refs *E2EClusterRefs
}

type E2EClusterRefs struct {
	Namespace      *corev1.Namespace
	NodeNetwork    *metalmodels.V1NetworkResponse
	Firewall       *metalmodels.V1FirewallResponse
	ControlPlaneIP *metalmodels.V1IPResponse

	Workload framework.ClusterProxy
	Cluster  *clusterv1.Cluster
}

func (e2e *E2ECluster) Variables() map[string]string {
	vars := make(map[string]string)

	for k := range e2e.E2EContext.E2EConfig.Variables {
		vars[k] = e2e.E2EContext.envOrVar(k)
	}

	vars["METAL_API_URL"] = e2e.E2EContext.envOrVar("METAL_API_URL")
	vars["METAL_API_HMAC"] = e2e.E2EContext.envOrVar("METAL_API_HMAC")
	vars["METAL_API_HMAC_AUTH_TYPE"] = e2e.E2EContext.envOrVar("METAL_API_HMAC_AUTH_TYPE")

	vars["NAMESPACE"] = e2e.NamespaceName
	vars["METAL_PROJECT_ID"] = e2e.E2EContext.Environment.projectID
	vars["METAL_PARTITION"] = e2e.E2EContext.Environment.partition
	vars["METAL_NODE_NETWORK_ID"] = *e2e.Refs.NodeNetwork.ID
	vars["FIREWALL_MACHINE_SIZE"] = e2e.FirewallSize
	vars["FIREWALL_MACHINE_IMAGE"] = e2e.FirewallImage
	vars["FIREWALL_MACHINE_NETWORKS"] = "[" + strings.Join(e2e.FirewallNetworks, ",") + "]"
	vars["CONTROL_PLANE_IP"] = e2e.ControlPlaneIP
	vars["CONTROL_PLANE_MACHINE_SIZE"] = e2e.ControlPlaneMachineSize
	vars["CONTROL_PLANE_MACHINE_IMAGE"] = e2e.ControlPlaneMachineImage
	vars["WORKER_MACHINE_SIZE"] = e2e.WorkerMachineSize
	vars["WORKER_MACHINE_IMAGE"] = e2e.WorkerMachineImage

	return vars
}

// common

func (e2e *E2ECluster) SetupNamespace(ctx context.Context) *corev1.Namespace {
	By("Setup Namespace for Cluster")

	ns := framework.CreateNamespace(ctx, framework.CreateNamespaceInput{
		Creator:             e2e.E2EContext.Environment.Bootstrap.GetClient(),
		Name:                e2e.NamespaceName,
		IgnoreAlreadyExists: true,
		Labels: map[string]string{
			"e2e-test":                  e2e.SpecName,
			e2eMetalStackProjectIDLabel: e2e.E2EContext.Environment.projectID,
		},
	})
	e2e.Refs.Namespace = ns
	return ns
}

func (e2e *E2ECluster) teardownNamespace(ctx context.Context) {
	if e2e.Refs.Namespace == nil {
		return
	}

	framework.DeleteNamespace(ctx, framework.DeleteNamespaceInput{
		Name:    e2e.Refs.Namespace.Name,
		Deleter: e2e.E2EContext.Environment.Bootstrap.GetClient(),
	})
	e2e.Refs.Namespace = nil
}

func (e2e *E2ECluster) SetupMetalStackPreconditions(ctx context.Context) {
	By("Setup Preconditions")
	e2e.setupNodeNetwork(ctx)
	e2e.setupFirewall(ctx)
	e2e.setupControlPlaneIP(ctx)
}

func (e2e *E2ECluster) Teardown(ctx context.Context) {
	e2e.teardownCluster(ctx)
	e2e.teardownControlPlaneIP(ctx)
	e2e.teardownFirewall(ctx)
	e2e.teardownNodeNetwork(ctx)
	e2e.teardownNamespace(ctx)
}

func (e2e *E2ECluster) setupNodeNetwork(ctx context.Context) {
	By("Setup Node Network")

	nar := &metalmodels.V1NetworkAllocateRequest{
		Partitionid: e2e.E2EContext.Environment.partition,
		Projectid:   e2e.E2EContext.Environment.projectID,
		Name:        e2e.ClusterName + "-node",
		Description: fmt.Sprintf("Node network for %s", e2e.ClusterName),
		Labels: map[string]string{
			"e2e-test":                            e2e.SpecName,
			capmsv1alpha1.TagInfraClusterResource: e2e.NamespaceName + "." + e2e.ClusterName,
		},
	}
	net, err := e2e.E2EContext.Environment.Metal.Network().AllocateNetwork(metalnetwork.NewAllocateNetworkParamsWithContext(ctx).WithBody(nar), nil)
	Expect(err).ToNot(HaveOccurred(), "failed to allocate node network")

	e2e.Refs.NodeNetwork = net.Payload
}

func (e2e *E2ECluster) teardownNodeNetwork(ctx context.Context) {
	if e2e.Refs.NodeNetwork == nil || e2e.Refs.NodeNetwork.ID == nil {
		return
	}

	_, err := e2e.E2EContext.Environment.Metal.Network().FreeNetwork(metalnetwork.NewFreeNetworkParamsWithContext(ctx).WithID(*e2e.Refs.NodeNetwork.ID), nil)
	Expect(err).ToNot(HaveOccurred(), "failed to delete node network")

	e2e.Refs.NodeNetwork = nil
}

func (e2e *E2ECluster) setupFirewall(ctx context.Context) {
	By("Setup Firewall")

	fcr := &metalmodels.V1FirewallCreateRequest{
		Name:        e2e.ClusterName + "-fw",
		Hostname:    e2e.ClusterName + "-fw",
		Description: "Firewall for " + e2e.ClusterName,
		Partitionid: &e2e.E2EContext.Environment.partition,
		Projectid:   &e2e.E2EContext.Environment.projectID,
		Sizeid:      &e2e.FirewallSize,
		Imageid:     &e2e.FirewallImage,
		Tags: []string{
			fmt.Sprintf("%s=%s.%s", capmsv1alpha1.TagInfraClusterResource, e2e.NamespaceName, e2e.ClusterName),
			fmt.Sprintf("%s=%s", "e2e-test", e2e.SpecName),
		},
		Networks: []*metalmodels.V1MachineAllocationNetwork{
			{
				Networkid: ptr.To(e2e.E2EContext.Environment.publicNetwork),
			},
			{
				Networkid: e2e.Refs.NodeNetwork.ID,
			},
		},
		// At the moment we just go with vastly broad firewall rules.
		// In production this should be limited down.
		FirewallRules: &metalmodels.V1FirewallRules{
			Egress: []*metalmodels.V1FirewallEgressRule{
				{
					Comment:  "allow outgoing HTTP and HTTPS traffic",
					Protocol: "TCP",
					Ports:    []int32{80, 443},
					To:       []string{"0.0.0.0/0"},
				},
				{
					Comment:  "allow outgoing DNS traffic via TCP",
					Protocol: "TCP",
					Ports:    []int32{53},
					To:       []string{"0.0.0.0/0"},
				},
				{
					Comment:  "allow outgoing traffic to control plane for ccm",
					Protocol: "TCP",
					Ports:    []int32{8080},
					To:       []string{"0.0.0.0/0"},
				},
				{
					Comment:  "allow outgoing DNS and NTP traffic via UDP",
					Protocol: "UDP",
					Ports:    []int32{53, 123},
					To:       []string{"0.0.0.0/0"},
				},
			},
			Ingress: []*metalmodels.V1FirewallIngressRule{
				{
					Comment:  "allow incoming HTTPS and HTTPS traffic",
					Protocol: "TCP",
					From:     []string{"0.0.0.0/0"},
					To:       []string{"0.0.0.0/0"},
					Ports:    []int32{80, 443},
				},
			},
		},
	}

	Eventually(func() error {
		fw, err := e2e.E2EContext.Environment.Metal.Firewall().AllocateFirewall(metalfw.NewAllocateFirewallParamsWithContext(ctx).WithBody(fcr), nil)
		if err != nil {
			return err
		}

		e2e.Refs.Firewall = fw.Payload
		return nil
	}, e2e.E2EContext.E2EConfig.GetIntervals("metal-stack", "wait-firewall-allocate")...).ShouldNot(HaveOccurred(), "firewall not available")

	GinkgoWriter.Printf("Firewall allocated with ID: %s\n", *e2e.Refs.Firewall.ID)
}

func (e2e *E2ECluster) teardownFirewall(ctx context.Context) {
	if e2e.Refs.Firewall == nil || e2e.Refs.Firewall.ID == nil {
		return
	}

	_, err := e2e.E2EContext.Environment.Metal.Machine().FreeMachine(metalmachine.NewFreeMachineParamsWithContext(ctx).WithID(*e2e.Refs.Firewall.ID), nil)
	Expect(err).ToNot(HaveOccurred(), "failed to free firewall machine")

	e2e.Refs.Firewall = nil
}

func (e2e *E2ECluster) setupControlPlaneIP(ctx context.Context) {
	if e2e.ControlPlaneIP != "" {
		return
	}

	By("Setup Control Plane IP")

	ipr := &metalmodels.V1IPAllocateRequest{
		Projectid:   &e2e.E2EContext.Environment.projectID,
		Name:        e2e.ClusterName + "-cp-ip",
		Description: "Control plane IP for " + e2e.ClusterName,
		Tags: []string{
			fmt.Sprintf("%s=%s.%s", capmsv1alpha1.TagInfraClusterResource, e2e.NamespaceName, e2e.ClusterName),
			fmt.Sprintf("%s=%s", "e2e-test", e2e.SpecName),
		},
		Networkid: ptr.To(e2e.E2EContext.Environment.publicNetwork),
		Type:      ptr.To(metalmodels.V1IPAllocateRequestTypeStatic),
	}

	ip, err := e2e.E2EContext.Environment.Metal.IP().AllocateIP(metalip.NewAllocateIPParamsWithContext(ctx).WithBody(ipr), nil)
	Expect(err).ToNot(HaveOccurred(), "failed to allocate control plane IP")
	Expect(ip.Payload.Ipaddress).NotTo(BeNil(), "allocated control plane IP has no IP address")

	e2e.Refs.ControlPlaneIP = ip.Payload
	e2e.ControlPlaneIP = *e2e.Refs.ControlPlaneIP.Ipaddress
}

func (e2e *E2ECluster) teardownControlPlaneIP(ctx context.Context) {
	if e2e.Refs.ControlPlaneIP == nil || e2e.Refs.ControlPlaneIP.Ipaddress == nil {
		return
	}

	_, err := e2e.E2EContext.Environment.Metal.IP().FreeIP(metalip.NewFreeIPParamsWithContext(ctx).WithID(*e2e.Refs.ControlPlaneIP.Ipaddress), nil)
	Expect(err).ToNot(HaveOccurred(), "failed to free control plane IP")

	e2e.Refs.ControlPlaneIP = nil
}

func (e2e *E2ECluster) GenerateAndApplyClusterTemplate(ctx context.Context) {
	By("Generate cluster template")

	Expect(e2e.Refs.Namespace).NotTo(BeNil(), "namespace not created yet")
	Expect(e2e.Refs.NodeNetwork).NotTo(BeNil(), "node network not created yet")
	Expect(e2e.Refs.Firewall).NotTo(BeNil(), "firewall not created yet")

	workloadTempl := clusterctl.ConfigCluster(ctx, clusterctl.ConfigClusterInput{
		Namespace:                e2e.NamespaceName,
		ClusterName:              e2e.ClusterName,
		KubernetesVersion:        e2e.KubernetesVersion,
		ControlPlaneMachineCount: &e2e.ControlPlaneMachineCount,
		WorkerMachineCount:       &e2e.WorkerMachineCount,
		ClusterctlConfigPath:     e2e.E2EContext.Environment.ClusterctlConfigPath,
		Flavor:                   e2e.E2EContext.Environment.Flavor,
		// TODO: why does this not work with clusterctl.DefaultInfrastructureProvider?
		InfrastructureProvider: "capms:v0.6.2",
		LogFolder:              path.Join(e2e.E2EContext.Environment.artifactsPath, "clusters", e2e.ClusterName),
		ClusterctlVariables:    e2e.Variables(),
	})

	By("Apply cluster template")
	err := e2e.E2EContext.Environment.Bootstrap.CreateOrUpdate(ctx, workloadTempl)
	Expect(err).NotTo(HaveOccurred(), "failed to apply cluster template")

	e2e.Refs.Workload = e2e.E2EContext.Environment.Bootstrap.GetWorkloadCluster(ctx, e2e.NamespaceName, e2e.ClusterName)

	err = copyFile(
		e2e.Refs.Workload.GetKubeconfigPath(),
		path.Join(e2e.E2EContext.Environment.artifactsPath, "clusters", e2e.ClusterName, "kubeconfig"),
	)
	Expect(err).NotTo(HaveOccurred(), "cannot copy workload kubeconfig file")

	e2e.Refs.Cluster = framework.DiscoveryAndWaitForCluster(ctx, framework.DiscoveryAndWaitForClusterInput{
		Namespace: e2e.NamespaceName,
		Name:      e2e.ClusterName,
		Getter:    e2e.E2EContext.Environment.Bootstrap.GetClient(),
	}, e2e.E2EContext.E2EConfig.GetIntervals("default", "wait-cluster")...)

	Expect(e2e.Refs.Cluster).NotTo(BeNil(), "failed to get cluster")
}

func (e2e *E2ECluster) teardownCluster(ctx context.Context) {
	if e2e.Refs.Cluster == nil {
		return
	}
	Expect(e2e.Refs.Cluster).NotTo(BeNil(), "cluster not created yet")

	deleteClusterAndWait(ctx, framework.DeleteClusterAndWaitInput{
		ClusterProxy:         e2e.E2EContext.Environment.Bootstrap,
		ClusterctlConfigPath: e2e.E2EContext.Environment.ClusterctlConfigPath,
		ArtifactFolder:       e2e.E2EContext.Environment.artifactsPath,
		Cluster:              e2e.Refs.Cluster,
	}, e2e.E2EContext.E2EConfig.GetIntervals("default", "wait-delete-cluster")...)
}

func (ec *E2ECluster) Dump(ctx context.Context) {
	framework.DumpResourcesForCluster(ctx, framework.DumpResourcesForClusterInput{
		Lister:  ec.E2EContext.Environment.Bootstrap.GetClient(),
		LogPath: path.Join(ec.E2EContext.Environment.artifactsPath, "clusters", ec.Refs.Cluster.Namespace+"_"+ec.Refs.Cluster.Name),
		Cluster: ec.Refs.Cluster,
		Resources: []framework.DumpNamespaceAndGVK{
			{
				GVK:       clusterv1.GroupVersion.WithKind("Cluster"),
				Namespace: ec.Refs.Cluster.Namespace,
			},
			{
				GVK:       capmsv1alpha1.GroupVersion.WithKind("MetalStackCluster"),
				Namespace: ec.Refs.Cluster.Namespace,
			},
			{
				GVK:       kubeadmvbootstrap1.GroupVersion.WithKind("KubeadmConfig"),
				Namespace: ec.Refs.Cluster.Namespace,
			},
			{
				GVK:       clusterv1.GroupVersion.WithKind("MachineDeployment"),
				Namespace: ec.Refs.Cluster.Namespace,
			},
			{
				GVK:       clusterv1.GroupVersion.WithKind("Machine"),
				Namespace: ec.Refs.Cluster.Namespace,
			},
			{
				GVK:       capmsv1alpha1.GroupVersion.WithKind("MetalStackMachine"),
				Namespace: ec.Refs.Cluster.Namespace,
			},
		},
	})
}

// deleteClusterAndWait deletes a cluster object and waits for it to be gone.
// TODO: remove once cluster expectation has been fixed in framework
func deleteClusterAndWait(ctx context.Context, input framework.DeleteClusterAndWaitInput, intervals ...any) {
	var (
		retryableOperationInterval = 3 * time.Second
		// retryableOperationTimeout requires a higher value especially for self-hosted upgrades.
		// Short unavailability of the Kube APIServer due to joining etcd members paired with unreachable conversion webhooks due to
		// failed leader election and thus controller restarts lead to longer taking retries.
		// The timeout occurs when listing machines in `GetControlPlaneMachinesByCluster`.
		retryableOperationTimeout = 3 * time.Minute
	)

	Expect(ctx).NotTo(BeNil(), "ctx is required for DeleteClusterAndWait")
	Expect(input.ClusterProxy).ToNot(BeNil(), "Invalid argument. input.ClusterProxy can't be nil when calling DeleteClusterAndWait")
	Expect(input.ClusterctlConfigPath).ToNot(BeNil(), "Invalid argument. input.ClusterctlConfigPath can't be nil when calling DeleteClusterAndWait")
	Expect(input.Cluster).ToNot(BeNil(), "Invalid argument. input.Cluster can't be empty when calling DeleteClusterAndWait")

	framework.DeleteCluster(ctx, framework.DeleteClusterInput{
		Deleter: input.ClusterProxy.GetClient(),
		Cluster: input.Cluster,
	})

	// log.Logf("Waiting for the Cluster object to be deleted")
	framework.WaitForClusterDeleted(ctx, framework.WaitForClusterDeletedInput(input), intervals...)

	// TODO: consider if to move in another func (what if there are more than one cluster?)
	// log.Logf("Check for all the Cluster API resources being deleted")
	Eventually(func() []*unstructured.Unstructured {
		return framework.GetCAPIResources(ctx, framework.GetCAPIResourcesInput{
			Lister:    input.ClusterProxy.GetClient(),
			Namespace: input.Cluster.Namespace,
		})
	}, retryableOperationTimeout, retryableOperationInterval).Should(BeEmpty(), "There are still Cluster API resources in the %q namespace", input.Cluster.Namespace)
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		Expect(sourceFile.Close()).ToNot(HaveOccurred(), "cannot close source file")
	}()

	destinationFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		Expect(destinationFile.Close()).ToNot(HaveOccurred(), "cannot close destination file")
	}()

	_, err = io.Copy(destinationFile, sourceFile)
	if err != nil {
		return err
	}

	return nil
}
