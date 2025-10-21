package frmwrk

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metal "github.com/metal-stack/metal-go"
	metalfw "github.com/metal-stack/metal-go/api/client/firewall"
	metalip "github.com/metal-stack/metal-go/api/client/ip"
	metalmachine "github.com/metal-stack/metal-go/api/client/machine"
	metalnetwork "github.com/metal-stack/metal-go/api/client/network"
	metalmodels "github.com/metal-stack/metal-go/api/models"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/bootstrap"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"

	capmsv1alpha1 "github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
)

type Option func(*E2EContext)

func NewE2EContext(options ...Option) *E2EContext {
	e2e := &E2EContext{
		E2EConfig: clusterctl.LoadE2EConfig(context.TODO(), clusterctl.LoadE2EConfigInput{
			ConfigPath: "config/capi-e2e-config.yaml",
		}),
		Environment: &Environment{},
	}

	e2e.Environment.Scheme = runtime.NewScheme()
	framework.TryAddDefaultSchemes(e2e.Environment.Scheme)
	_ = capmsv1alpha1.AddToScheme(e2e.Environment.Scheme)
	// e2e.Environment.Namespaces = map[*corev1.Namespace]context.CancelFunc{}

	withDefaultEnvironment()(e2e)
	for _, option := range options {
		option(e2e)
	}

	return e2e
}

func (e2e *E2EContext) envOrVar(name string) string {
	val := os.Getenv(name)
	if val == "" {
		val = e2e.E2EConfig.GetVariableOrEmpty(name)
	}
	Expect(val).ToNot(BeEmpty(), fmt.Sprintf("%s must be set in e2e config or environment", name))

	return val
}

func withDefaultEnvironment() Option {
	return func(e2e *E2EContext) {
		e2e.Environment.kubeconfigPath = os.Getenv("E2E_KUBECONFIG")

		if path, ok := os.LookupEnv("ARTIFACTS"); ok {
			e2e.Environment.artifactsPath = path
		} else {
			e2e.Environment.artifactsPath = "artifacts"
		}

		cwd, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred(), "cannot get current working directory")

		e2e.Environment.artifactsPath = path.Join(cwd, e2e.Environment.artifactsPath)
		Expect(os.MkdirAll(e2e.Environment.artifactsPath, 0755)).To(Succeed(), "failed to create artifact folder")

		clustersFolder := path.Join(e2e.Environment.artifactsPath, "clusters")
		Expect(os.MkdirAll(clustersFolder, 0755)).To(Succeed(), "failed to create clusters folder")

		bootstrapFolder := path.Join(clustersFolder, "bootstrap")
		Expect(os.MkdirAll(bootstrapFolder, 0755)).To(Succeed(), "failed to create bootstrap folder")

		mclient, err := metal.NewDriver(
			e2e.envOrVar("METAL_API_URL"),
			"",
			e2e.envOrVar("METAL_API_HMAC"),
			metal.AuthType(e2e.envOrVar("METAL_API_HMAC_AUTH_TYPE")),
		)
		Expect(err).ToNot(HaveOccurred(), "failed to create metal client")
		e2e.Environment.Metal = mclient

		e2e.Environment.project = e2e.envOrVar("METAL_PROJECT_ID")
		e2e.Environment.partition = e2e.envOrVar("METAL_PARTITION")
		e2e.Environment.publicNetwork = e2e.envOrVar("METAL_PUBLIC_NETWORK")
		e2e.Environment.kubernetesVersions = strings.Split(e2e.envOrVar("E2E_KUBERNETES_VERSIONS"), ",")
		e2e.Environment.controlPlaneMachineImagePrefix = e2e.envOrVar("E2E_CONTROL_PLANE_MACHINE_IMAGE_PREFIX")
		e2e.Environment.workerMachineImagePrefix = e2e.envOrVar("E2E_WORKER_MACHINE_IMAGE_PREFIX")

		_ = e2e.envOrVar("CONTROL_PLANE_MACHINE_SIZE")
		_ = e2e.envOrVar("WORKER_MACHINE_SIZE")
		_ = e2e.envOrVar("FIREWALL_IMAGE")
		_ = e2e.envOrVar("FIREWALL_SIZE")
		_ = e2e.envOrVar("FIREWALL_NETWORKS")
	}
}

// E2EContext holds the shared context for the e2e tests.
type E2EContext struct {
	E2EConfig   *clusterctl.E2EConfig
	Environment *Environment
}

type Environment struct {
	Scheme               *runtime.Scheme
	ClusterProvider      bootstrap.ClusterProvider
	Bootstrap            framework.ClusterProxy
	Metal                metal.Client
	ClusterctlConfigPath string

	kubernetesVersions             []string
	controlPlaneMachineImagePrefix string
	workerMachineImagePrefix       string
	partition                      string
	project                        string
	publicNetwork                  string
	kubeconfigPath                 string
	artifactsPath                  string
}

func (ee *E2EContext) ProvideBootstrapCluster() {
	By("Provisioning bootstrap cluster")

	var kube string

	if ee.Environment.kubeconfigPath != "" {
		kube = ee.provideExistingBootstrapClusterKubeconfig()
	} else {
		kube = ee.provideKindBootstrapClusterKubeconfig()
	}

	kubeconf, err := os.Open(kube)
	Expect(err).NotTo(HaveOccurred(), "cannot open kubeconfig")

	bootstrapPath := path.Join(ee.Environment.artifactsPath, "clusters", "bootstrap", "kubeconfig")
	bootstrap, err := os.OpenFile(bootstrapPath, os.O_CREATE|os.O_WRONLY, 0644)
	Expect(err).NotTo(HaveOccurred(), "cannot create bootstrap kubeconfig file")

	_, err = io.Copy(bootstrap, kubeconf)
	Expect(err).ToNot(HaveOccurred(), "cannot copy kubeconfig content")
	Expect(bootstrap.Close()).To(Succeed(), "cannot close bootstrap kubeconfig file")
	Expect(kubeconf.Close()).To(Succeed(), "cannot close kubeconfig file")

	ee.Environment.Bootstrap = framework.NewClusterProxy("bootstrap", bootstrapPath, ee.Environment.Scheme)
}

func (ee *E2EContext) provideExistingBootstrapClusterKubeconfig() string {
	Expect(ee.Environment.kubeconfigPath).ToNot(BeEmpty(), "E2E_KUBECONFIG must be set to use an existing bootstrap cluster")
	Expect(ee.Environment.kubeconfigPath).To(BeAnExistingFile(), "E2E_KUBECONFIG must point to an existing file")
	return ee.Environment.kubeconfigPath
}

func (ee *E2EContext) provideKindBootstrapClusterKubeconfig() string {
	bootstrapFolder := path.Join(ee.Environment.artifactsPath, "clusters", "bootstrap")
	bootstrapPro := bootstrap.CreateKindBootstrapClusterAndLoadImages(context.TODO(), bootstrap.CreateKindBootstrapClusterAndLoadImagesInput{
		Name:      "bootstrap",
		LogFolder: bootstrapFolder,
		Images:    ee.E2EConfig.Images,
	})
	ee.Environment.ClusterProvider = bootstrapPro
	return bootstrapPro.GetKubeconfigPath()
}

func (ee *E2EContext) CreateClusterctlConfig(ctx context.Context) {
	By("Create clusterctl repository config")
	Expect(ee.Environment.Bootstrap).NotTo(BeNil(), "bootstrap cluster must be provided first")

	createRepoInput := clusterctl.CreateRepositoryInput{
		RepositoryFolder: path.Join(ee.Environment.artifactsPath, "repository"),
		E2EConfig:        ee.E2EConfig,
	}

	ee.Environment.ClusterctlConfigPath = clusterctl.CreateRepository(ctx, createRepoInput)
	Expect(ee.Environment.ClusterctlConfigPath).To(BeAnExistingFile(), "clusterctl config file doesn't exist")
}

func (ee *E2EContext) InitManagementCluster(ctx context.Context) {
	By("Init Management Cluster")
	Expect(ee.Environment.Bootstrap).NotTo(BeNil(), "bootstrap cluster must be provided first")
	Expect(ee.Environment.ClusterctlConfigPath).To(BeAnExistingFile(), "clusterctl config file doesn't exist")

	clusterctl.InitManagementClusterAndWatchControllerLogs(ctx, clusterctl.InitManagementClusterAndWatchControllerLogsInput{
		ClusterProxy:            ee.Environment.Bootstrap,
		ClusterctlConfigPath:    ee.Environment.ClusterctlConfigPath,
		InfrastructureProviders: ee.E2EConfig.InfrastructureProviders(),
		LogFolder:               path.Join(ee.Environment.artifactsPath, "clusters", "bootstrap"),
	})
}

func (ee *E2EContext) Teardown(ctx context.Context) {
	if ee.Environment.ClusterProvider != nil {
		ee.Environment.ClusterProvider.Dispose(ctx)
		ee.Environment.ClusterProvider = nil
	}
}

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

func (e2e *E2EContext) NewE2ECluster(cfg ClusterConfig) *E2ECluster {
	Expect(cfg.ClusterName).ToNot(BeEmpty(), "ClusterName must be set")
	Expect(cfg.NamespaceName).ToNot(BeEmpty(), "NamespaceName must be set")
	Expect(cfg.SpecName).ToNot(BeEmpty(), "SpecName must be set")
	Expect(cfg.KubernetesVersion).ToNot(BeEmpty(), "KubernetesVersion must be set")

	if cfg.FirewallImage == "" {
		cfg.FirewallImage = e2e.envOrVar("FIREWALL_IMAGE")
	}
	if cfg.FirewallNetworks == nil {
		cfg.FirewallNetworks = strings.Split(e2e.envOrVar("FIREWALL_NETWORKS"), ",")
	}
	if cfg.FirewallSize == "" {
		cfg.FirewallSize = e2e.envOrVar("FIREWALL_SIZE")
	}

	if cfg.ControlPlaneMachineImage == "" {
		cfg.ControlPlaneMachineImage = e2e.envOrVar("CONTROL_PLANE_MACHINE_IMAGE")
	}
	if cfg.ControlPlaneMachineSize == "" {
		cfg.ControlPlaneMachineSize = e2e.envOrVar("CONTROL_PLANE_MACHINE_SIZE")
	}

	if cfg.WorkerMachineImage == "" {
		cfg.WorkerMachineImage = e2e.envOrVar("WORKER_MACHINE_IMAGE")
	}
	if cfg.WorkerMachineSize == "" {
		cfg.WorkerMachineSize = e2e.envOrVar("WORKER_MACHINE_SIZE")
	}

	return &E2ECluster{
		E2EContext:    e2e,
		ClusterConfig: cfg,
		Refs:          &E2EClusterRefs{},
	}
}

// common

func (e2e *E2ECluster) SetupNamespace(ctx context.Context) *corev1.Namespace {
	By("Setup Namespace for Cluster")

	ns := framework.CreateNamespace(ctx, framework.CreateNamespaceInput{
		Creator:             e2e.E2EContext.Environment.Bootstrap.GetClient(),
		Name:                e2e.NamespaceName,
		IgnoreAlreadyExists: true,
		Labels: map[string]string{
			"e2e-test": e2e.SpecName,
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
		Name: e2e.Refs.Namespace.Name,
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
	e2e.teardownClusterResourceSets(ctx)
	e2e.teardownControlPlaneIP(ctx)
	e2e.teardownFirewall(ctx)
	e2e.teardownNodeNetwork(ctx)
	e2e.teardownNamespace(ctx)
}

func (e2e *E2ECluster) setupNodeNetwork(ctx context.Context) {
	By("Setup Node Network")

	nar := &metalmodels.V1NetworkAllocateRequest{
		Partitionid: e2e.E2EContext.Environment.partition,
		Projectid:   e2e.E2EContext.Environment.project,
		Name:        e2e.ClusterName + "-node",
		Description: fmt.Sprintf("Node network for %s", e2e.ClusterName),
		Labels: map[string]string{
			"e2e-test": e2e.SpecName,
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
		Projectid:   &e2e.E2EContext.Environment.project,
		Sizeid:      &e2e.FirewallSize,
		Imageid:     &e2e.FirewallImage,
		Tags: []string{
			fmt.Sprintf("%s=%s", capmsv1alpha1.TagInfraClusterResource, e2e.ClusterName),
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

	fw, err := e2e.E2EContext.Environment.Metal.Firewall().AllocateFirewall(metalfw.NewAllocateFirewallParamsWithContext(ctx).WithBody(fcr), nil)
	Expect(err).ToNot(HaveOccurred(), "failed to allocate firewall")

	e2e.Refs.Firewall = fw.Payload
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
	By("Setup Control Plane IP")

	ipr := &metalmodels.V1IPAllocateRequest{
		Projectid:   &e2e.E2EContext.Environment.project,
		Name:        e2e.ClusterName + "-cp-ip",
		Description: "Control plane IP for " + e2e.ClusterName,
		Tags: []string{
			fmt.Sprintf("%s=%s", capmsv1alpha1.TagInfraClusterResource, e2e.ClusterName),
			fmt.Sprintf("%s=%s", "e2e-test", e2e.SpecName),
		},
		Networkid: ptr.To(e2e.E2EContext.Environment.publicNetwork),
	}

	ip, err := e2e.E2EContext.Environment.Metal.IP().AllocateIP(metalip.NewAllocateIPParamsWithContext(ctx).WithBody(ipr), nil)
	Expect(err).ToNot(HaveOccurred(), "failed to allocate control plane IP")

	e2e.Refs.ControlPlaneIP = ip.Payload
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
		// TODO: why does this not work with clusterctl.DefaultInfrastructureProvider?
		InfrastructureProvider: "capms:v0.6.1",
		LogFolder:              path.Join(e2e.E2EContext.Environment.artifactsPath, "clusters", e2e.ClusterName),
		// KubeconfigPath:         "",
		ClusterctlVariables: map[string]string{
			// "METAL_API_URL":               "",
			// "METAL_API_HMAC":              "",
			"METAL_PROJECT_ID": e2e.E2EContext.Environment.project,
			// "POD_CIDR":                    "",
			"METAL_PARTITION":             e2e.E2EContext.Environment.partition,
			"METAL_NODE_NETWORK_ID":       *e2e.Refs.NodeNetwork.ID,
			"FIREWALL_MACHINE_SIZE":       e2e.FirewallSize,
			"FIREWALL_MACHINE_IMAGE":      e2e.FirewallImage,
			"FIREWALL_MACHINE_NETWORKS":   "[" + strings.Join(e2e.FirewallNetworks, ",") + "]",
			"CONTROL_PLANE_IP":            e2e.ControlPlaneIP,
			"CONTROL_PLANE_MACHINE_SIZE":  e2e.ControlPlaneMachineSize,
			"CONTROL_PLANE_MACHINE_IMAGE": e2e.ControlPlaneMachineImage,
			"WORKER_MACHINE_SIZE":         e2e.WorkerMachineSize,
			"WORKER_MACHINE_IMAGE":        e2e.WorkerMachineImage,
		},
	})

	By("Apply cluster template")
	err := e2e.E2EContext.Environment.Bootstrap.CreateOrUpdate(ctx, workloadTempl)
	Expect(err).NotTo(HaveOccurred(), "failed to apply cluster template")

	e2e.Refs.Workload = e2e.E2EContext.Environment.Bootstrap.GetWorkloadCluster(ctx, e2e.NamespaceName, e2e.ClusterName)

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

func (e2e *E2ECluster) teardownClusterResourceSets(ctx context.Context) {
	sets := framework.GetClusterResourceSets(ctx, framework.GetClusterResourceSetsInput{
		Lister:    e2e.E2EContext.Environment.Bootstrap.GetClient(),
		Namespace: e2e.NamespaceName,
	})

	for _, s := range sets {
		err := e2e.E2EContext.Environment.Bootstrap.GetClient().Delete(ctx, s)
		if err != nil && !apierrors.IsNotFound(err) {
			Expect(err).NotTo(HaveOccurred(), "failed to delete cluster resourceset")
		}
	}
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
