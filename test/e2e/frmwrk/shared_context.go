package frmwrk

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	ginkgotypes "github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega" //nolint:staticcheck

	metal "github.com/metal-stack/metal-go"
	"github.com/metal-stack/metal-go/api/client/ip"
	"github.com/metal-stack/metal-go/api/client/machine"
	"github.com/metal-stack/metal-go/api/client/network"
	"github.com/metal-stack/metal-go/api/models"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/bootstrap"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/controller-runtime/pkg/client"

	capmsv1alpha1 "github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
)

const e2eMetalStackProjectIDLabel = "e2e-metal-stack-project-id"

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

		if p, ok := os.LookupEnv("ARTIFACTS"); ok {
			e2e.Environment.artifactsPath = p
		} else {
			e2e.Environment.artifactsPath = "artifacts"
		}

		cwd, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred(), "cannot get current working directory")

		if !path.IsAbs(e2e.Environment.artifactsPath) {
			e2e.Environment.artifactsPath = path.Join(cwd, e2e.Environment.artifactsPath)
		}
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

		e2e.Environment.projectID = e2e.envOrVar("METAL_PROJECT_ID")
		e2e.Environment.projectName = e2e.envOrVar("E2E_METAL_PROJECT_NAME")
		e2e.Environment.partition = e2e.envOrVar("METAL_PARTITION")
		e2e.Environment.publicNetwork = e2e.envOrVar("METAL_PUBLIC_NETWORK")
		e2e.Environment.kubernetesVersions = strings.Split(e2e.envOrVar("E2E_KUBERNETES_VERSIONS"), ",")
		e2e.Environment.controlPlaneMachineImagePrefix = e2e.envOrVar("E2E_CONTROL_PLANE_MACHINE_IMAGE_PREFIX")
		e2e.Environment.workerMachineImagePrefix = e2e.envOrVar("E2E_WORKER_MACHINE_IMAGE_PREFIX")
		e2e.Environment.Flavor = e2e.envOrVar("E2E_DEFAULT_FLAVOR")

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
	Flavor               string

	kubernetesVersions             []string
	controlPlaneMachineImagePrefix string
	workerMachineImagePrefix       string
	partition                      string
	projectID                      string
	projectName                    string
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
	bootstrapKubeconf, err := os.OpenFile(bootstrapPath, os.O_CREATE|os.O_WRONLY, 0644)
	Expect(err).NotTo(HaveOccurred(), "cannot create bootstrap kubeconfig file")

	_, err = io.Copy(bootstrapKubeconf, kubeconf)
	Expect(err).ToNot(HaveOccurred(), "cannot copy kubeconfig content")
	Expect(bootstrapKubeconf.Close()).To(Succeed(), "cannot close bootstrap kubeconfig file")
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

func (ee *E2EContext) projectNamespacePrefix() string {
	return fmt.Sprintf("e2e-%s-", ee.Environment.projectName)
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
		AddonProviders:          ee.E2EConfig.AddonProviders(),
		LogFolder:               path.Join(ee.Environment.artifactsPath, "clusters", "bootstrap"),
	})
}

func (ee *E2EContext) Teardown(ctx context.Context) {
	if ee.Environment.ClusterProvider != nil {
		ee.Environment.ClusterProvider.Dispose(ctx)
		ee.Environment.ClusterProvider = nil
	}
}

func (ee *E2EContext) TeardownMetalStackProject(ctx context.Context) {
	filter, err := ginkgotypes.ParseLabelFilter(GinkgoLabelFilter())
	Expect(err).ToNot(HaveOccurred(), "failed to parse ginkgo label filter")

	if !filter([]string{"teardown"}) {
		return
	}

	allMachinesInUse, err := ee.Environment.Metal.Machine().FindMachines(machine.NewFindMachinesParamsWithContext(ctx).WithBody(&models.V1MachineFindRequest{
		PartitionID:       ee.Environment.partition,
		AllocationProject: ee.Environment.projectID,
	}), nil)
	Expect(err).ToNot(HaveOccurred(), "failed to list metal machines for project")

	if ee.Environment.Bootstrap != nil {
		By("Cleanup all remaining clusters")

		var namespaces corev1.NamespaceList
		err := ee.Environment.Bootstrap.GetClient().List(ctx, &namespaces, client.MatchingLabels{
			e2eMetalStackProjectIDLabel: ee.Environment.projectID,
		})
		Expect(err).ToNot(HaveOccurred(), "failed to list namespaces in bootstrap cluster")

		for _, ns := range namespaces.Items {
			By(fmt.Sprintf("Deleting all clusters in namespace %s", ns.Name))
			framework.DeleteAllClustersAndWait(ctx, framework.DeleteAllClustersAndWaitInput{
				ClusterProxy:         ee.Environment.Bootstrap,
				ClusterctlConfigPath: ee.Environment.ClusterctlConfigPath,
				Namespace:            ns.Name,
				ArtifactFolder:       path.Join(ee.Environment.artifactsPath, "clusters", "bootstrap"),
			}, ee.E2EConfig.GetIntervals("default", "wait-delete-cluster")...)

			resources := framework.GetCAPIResources(ctx, framework.GetCAPIResourcesInput{
				Lister:    ee.Environment.Bootstrap.GetClient(),
				Namespace: ns.Name,
				IncludeTypes: []metav1.TypeMeta{
					{
						Kind:       "HelmReleaseProxy",
						APIVersion: "addons.cluster.x-k8s.io/v1alpha1",
					},
					{
						Kind:       "HelmChartProxy",
						APIVersion: "addons.cluster.x-k8s.io/v1alpha1",
					},
					{
						Kind:       "ClusterResourceSetBinding",
						APIVersion: "addons.cluster.x-k8s.io/v1beta1",
					},
					{
						Kind:       "ClusterResourceSet",
						APIVersion: "addons.cluster.x-k8s.io/v1beta1",
					},
				},
			})

			for _, r := range resources {
				err := ee.Environment.Bootstrap.GetClient().Delete(ctx, r)
				Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed to delete resource %s/%s of kind %s", r.GetNamespace(), r.GetName(), r.GetObjectKind().GroupVersionKind().Kind))
			}

			framework.DeleteNamespace(ctx, framework.DeleteNamespaceInput{
				Deleter: ee.Environment.Bootstrap.GetClient(),
				Name:    ns.Name,
			})
		}
	}

	isE2ETestTag := func(tag string) bool {
		return strings.HasPrefix(tag, capmsv1alpha1.TagInfraClusterResource+"="+ee.projectNamespacePrefix())
	}

	By("Cleanup all remaining machines")
	ms, err := ee.Environment.Metal.Machine().FindMachines(machine.NewFindMachinesParamsWithContext(ctx).WithBody(&models.V1MachineFindRequest{
		PartitionID:       ee.Environment.partition,
		AllocationProject: ee.Environment.projectID,
	}), nil)
	Expect(err).ToNot(HaveOccurred(), "failed to list metal machines for project")

	for _, m := range ms.Payload {
		if !slices.ContainsFunc(m.Tags, isE2ETestTag) {
			continue
		}
		By(fmt.Sprintf("Freeing machine %s", *m.ID))
		_, err := ee.Environment.Metal.Machine().FreeMachine(machine.NewFreeMachineParamsWithContext(ctx).WithID(*m.ID), nil)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("failed to free metal machine %s", *m.ID))
	}

	By("Cleanup all remaining IPs")
	ips, err := ee.Environment.Metal.IP().FindIPs(ip.NewFindIPsParamsWithContext(ctx).WithBody(&models.V1IPFindRequest{
		Projectid: ee.Environment.projectID,
	}), nil)
	Expect(err).ToNot(HaveOccurred(), "failed to list metal IPs for project")

	for _, addr := range ips.Payload {
		if !slices.ContainsFunc(addr.Tags, isE2ETestTag) {
			continue
		}
		_, err := ee.Environment.Metal.IP().FreeIP(ip.NewFreeIPParamsWithContext(ctx).WithID(*addr.Ipaddress), nil)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("failed to free metal IP %s", *addr.Ipaddress))
	}

	By("Cleanup all remaining networks")
	nets, err := ee.Environment.Metal.Network().FindNetworks(network.NewFindNetworksParamsWithContext(ctx).WithBody(&models.V1NetworkFindRequest{
		Projectid: ee.Environment.projectID,
	}), nil)
	Expect(err).ToNot(HaveOccurred(), "failed to list metal networks for project")

	for _, net := range nets.Payload {
		if label, ok := net.Labels[capmsv1alpha1.TagInfraClusterResource]; !ok || !strings.HasPrefix(label, ee.projectNamespacePrefix()) {
			continue
		}
		_, err := ee.Environment.Metal.Network().FreeNetwork(network.NewFreeNetworkParamsWithContext(ctx).WithID(*net.ID), nil)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("failed to free metal network %s", *net.ID))
	}

	By("Wait for all machines to be waiting")
	for _, m := range allMachinesInUse.Payload {
		if !slices.ContainsFunc(m.Tags, isE2ETestTag) {
			continue
		}
		By(fmt.Sprintf("Waiting for machine %s", *m.ID))
		Eventually(ctx, func(g Gomega) {
			mr, err := ee.Environment.Metal.Machine().FindMachine(machine.NewFindMachineParamsWithContext(ctx).WithID(*m.ID), nil)
			g.Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("failed to get metal machine %s", *m.ID))

			event := mr.Payload.Events.Log[0]
			g.Expect(event.Event).ToNot(BeNil(), "machine event is nil")
			g.Expect(event.Event).To(HaveValue(Equal("Waiting")), "machine is not in waiting state")
		}, "10m", "10s").WithContext(ctx).Should(Succeed(), "timed out waiting for machine %s to be in waiting state", *m.ID)
	}
}

func (e2e *E2EContext) NewE2ECluster(cfg ClusterConfig) *E2ECluster {
	Expect(cfg.ClusterName).ToNot(BeEmpty(), "ClusterName must be set")
	Expect(cfg.NamespaceName).ToNot(BeEmpty(), "NamespaceName must be set")
	Expect(cfg.SpecName).ToNot(BeEmpty(), "SpecName must be set")
	Expect(cfg.KubernetesVersion).ToNot(BeEmpty(), "KubernetesVersion must be set")

	cfg.NamespaceName = e2e.projectNamespacePrefix() + cfg.NamespaceName

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
