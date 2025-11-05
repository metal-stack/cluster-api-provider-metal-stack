package frmwrk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	. "github.com/onsi/gomega"    //nolint:staticcheck

	capi_e2e "sigs.k8s.io/cluster-api/test/e2e"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/cluster-api/test/framework/kubetest"
	"sigs.k8s.io/cluster-api/util"
)

var _ = Describe("Upgrade Kubernetes Cluster Version", Ordered, Label("upgrade"), func() {
	var (
		ec                    *E2ECluster
		ctx                   = context.TODO()
		fromKubernetesVersion string
		toKubernetesVersion   string
		cfg                   *clusterctl.E2EConfig
	)

	BeforeAll(func() {
		fromKubernetesVersion = e2eCtx.envOrVar("KUBERNETES_VERSION_UPGRADE_FROM")
		toKubernetesVersion = e2eCtx.envOrVar("KUBERNETES_VERSION_UPGRADE_TO")

		ec = e2eCtx.NewE2ECluster(ClusterConfig{
			SpecName:                 "cluster-upgrade",
			NamespaceName:            "cluster-upgrade",
			ClusterName:              "cluster-upgrade",
			KubernetesVersion:        fromKubernetesVersion,
			ControlPlaneMachineImage: e2eCtx.envOrVar("E2E_CONTROL_PLANE_MACHINE_IMAGE_PREFIX") + fromKubernetesVersion,
			ControlPlaneMachineCount: 1,
			WorkerMachineImage:       e2eCtx.envOrVar("E2E_WORKER_MACHINE_IMAGE_PREFIX") + fromKubernetesVersion,
			WorkerMachineCount:       0,
		})
		ec.SetupMetalStackPreconditions(ctx)
		ec.SetupNamespace(ctx)

		cfg = e2eCtx.E2EConfig.DeepCopy()

		Expect(cfg.Variables).NotTo(BeNil(), "E2E config variables map must be initialized")

		cfg.Variables = ec.Variables()
		cfg.Variables["KUBERNETES_VERSION_UPGRADE_FROM"] = fromKubernetesVersion
		cfg.Variables["KUBERNETES_VERSION_UPGRADE_TO"] = toKubernetesVersion
	})

	Context("rolling upgrade", func() {
		capi_e2e_ClusterUpgradeConformanceSpec(ctx, func() capi_e2e.ClusterUpgradeConformanceSpecInput {
			Expect(cfg.Variables).NotTo(BeNil(), "E2E config variables map must be initialized")
			Expect(cfg.Variables).To(HaveKey("CONTROL_PLANE_IP"))
			Expect(cfg.Variables).To(HaveKey("METAL_NODE_NETWORK_ID"))
			return capi_e2e.ClusterUpgradeConformanceSpecInput{
				E2EConfig:                cfg,
				ClusterctlConfigPath:     e2eCtx.Environment.ClusterctlConfigPath,
				BootstrapClusterProxy:    e2eCtx.Environment.Bootstrap,
				ArtifactFolder:           e2eCtx.Environment.artifactsPath,
				SkipCleanup:              true,
				SkipConformanceTests:     true,
				ControlPlaneMachineCount: ptr.To[int64](1),
				WorkerMachineCount:       ptr.To[int64](0),
				Flavor:                   ptr.To("upgrade"),
				ControlPlaneWaiters:      clusterctl.ControlPlaneWaiters{},
				InfrastructureProvider:   new(string),
				PostNamespaceCreated: func(managementClusterProxy framework.ClusterProxy, workloadClusterNamespace string) {
					ec.NamespaceName = workloadClusterNamespace
					ec.Refs.Namespace = &corev1.Namespace{
						ObjectMeta: metav1.ObjectMeta{
							Name: workloadClusterNamespace,
						},
					}
					err := ec.E2EContext.Environment.Bootstrap.GetClient().Get(ctx, util.ObjectKey(ec.Refs.Namespace), ec.Refs.Namespace)
					Expect(err).NotTo(HaveOccurred(), "Failed to get workload cluster namespace %s", ec.NamespaceName)

					ec.Refs.Namespace.SetLabels(map[string]string{
						e2eMetalStackProjectIDLabel: ec.E2EContext.Environment.projectID,
					})
					err = ec.E2EContext.Environment.Bootstrap.GetClient().Update(ctx, ec.Refs.Namespace)
					Expect(err).NotTo(HaveOccurred(), "Failed to add e2e project label workload cluster namespace %s", ec.NamespaceName)
				},
				PreWaitForControlPlaneToBeUpgraded: func(managementClusterProxy framework.ClusterProxy, workloadClusterNamespace string, workloadClusterName string) {
					ec.NamespaceName = workloadClusterNamespace
					ec.ClusterName = workloadClusterName
					ec.Refs.Workload = ec.E2EContext.Environment.Bootstrap.GetWorkloadCluster(ctx, ec.NamespaceName, ec.ClusterName)
					ec.Refs.Cluster = framework.GetClusterByName(ctx, framework.GetClusterByNameInput{
						Getter:    ec.E2EContext.Environment.Bootstrap.GetClient(),
						Name:      workloadClusterName,
						Namespace: workloadClusterNamespace,
					})
				},
			}
		})
	})

	It("delete cluster", Label("upgrade", "teardown"), func() {
		if ec == nil {
			Skip("E2ECluster not initialized, skipping teardown")
		}

		ec.Teardown(ctx)
	})
})

// TODO: remove once merged into upstream
// https://github.com/kubernetes-sigs/cluster-api/pull/12949
//
// capi_e2e_ClusterUpgradeConformanceSpec implements a spec that upgrades a cluster and runs the Kubernetes conformance suite.
// Upgrading a cluster refers to upgrading the control-plane and worker nodes (managed by MD and machine pools).
// NOTE: This test only works with a KubeadmControlPlane.
// NOTE: This test works with Clusters with and without ClusterClass.
// When using ClusterClass the ClusterClass must have the variables "etcdImageTag" and "coreDNSImageTag" of type string.
// Those variables should have corresponding patches which set the etcd and CoreDNS tags in KCP.
func capi_e2e_ClusterUpgradeConformanceSpec(ctx context.Context, inputGetter func() capi_e2e.ClusterUpgradeConformanceSpecInput) {
	const (
		kubetestConfigurationVariable = "KUBETEST_CONFIGURATION"
		specName                      = "k8s-upgrade-and-conformance"
	)

	var (
		input         capi_e2e.ClusterUpgradeConformanceSpecInput
		namespace     *corev1.Namespace
		cancelWatches context.CancelFunc

		controlPlaneMachineCount int64
		workerMachineCount       int64

		etcdVersionUpgradeTo    string
		coreDNSVersionUpgradeTo string

		clusterResources       *clusterctl.ApplyClusterTemplateAndWaitResult
		kubetestConfigFilePath string

		clusterName string
	)

	BeforeEach(func() {
		Expect(ctx).NotTo(BeNil(), "ctx is required for %s spec", specName)
		input = inputGetter()
		Expect(input.E2EConfig).ToNot(BeNil(), "Invalid argument. input.E2EConfig can't be nil when calling %s spec", specName)
		Expect(input.ClusterctlConfigPath).To(BeAnExistingFile(), "Invalid argument. input.ClusterctlConfigPath must be an existing file when calling %s spec", specName)
		Expect(input.BootstrapClusterProxy).ToNot(BeNil(), "Invalid argument. input.BootstrapClusterProxy can't be nil when calling %s spec", specName)
		Expect(os.MkdirAll(input.ArtifactFolder, 0750)).To(Succeed(), "Invalid argument. input.ArtifactFolder can't be created for %s spec", specName)

		Expect(input.E2EConfig.Variables).To(HaveKey(capi_e2e.KubernetesVersionUpgradeFrom))
		Expect(input.E2EConfig.Variables).To(HaveKey(capi_e2e.KubernetesVersionUpgradeTo))

		Expect(input.E2EConfig.Variables).To(HaveKey(kubetestConfigurationVariable), "% spec requires a %s variable to be defined in the config file", specName, kubetestConfigurationVariable)
		kubetestConfigFilePath = input.E2EConfig.MustGetVariable(kubetestConfigurationVariable)
		Expect(kubetestConfigFilePath).To(BeAnExistingFile(), "%s should be a valid kubetest config file")

		if input.ControlPlaneMachineCount == nil {
			controlPlaneMachineCount = 1
		} else {
			controlPlaneMachineCount = *input.ControlPlaneMachineCount
		}

		if input.WorkerMachineCount == nil {
			workerMachineCount = 2
		} else {
			workerMachineCount = *input.WorkerMachineCount
		}

		etcdVersionUpgradeTo = input.E2EConfig.GetVariableOrEmpty(capi_e2e.EtcdVersionUpgradeTo)
		coreDNSVersionUpgradeTo = input.E2EConfig.GetVariableOrEmpty(capi_e2e.CoreDNSVersionUpgradeTo)

		// Setup a Namespace where to host objects for this spec and create a watcher for the Namespace events.
		namespace, cancelWatches = framework.SetupSpecNamespace(ctx, specName, input.BootstrapClusterProxy, input.ArtifactFolder, input.PostNamespaceCreated)
		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
	})

	It("Should create and upgrade a workload cluster and eventually run kubetest", func() {
		By("Creating a workload cluster")

		infrastructureProvider := clusterctl.DefaultInfrastructureProvider
		if input.InfrastructureProvider != nil {
			infrastructureProvider = *input.InfrastructureProvider
		}

		clusterName = fmt.Sprintf("%s-%s", specName, util.RandomString(6))

		clusterctl.ApplyClusterTemplateAndWait(ctx, clusterctl.ApplyClusterTemplateAndWaitInput{
			ClusterProxy: input.BootstrapClusterProxy,
			ConfigCluster: clusterctl.ConfigClusterInput{
				LogFolder:                filepath.Join(input.ArtifactFolder, "clusters", input.BootstrapClusterProxy.GetName()),
				ClusterctlConfigPath:     input.ClusterctlConfigPath,
				ClusterctlVariables:      input.E2EConfig.Variables, // TODO: PR to upstream
				KubeconfigPath:           input.BootstrapClusterProxy.GetKubeconfigPath(),
				InfrastructureProvider:   infrastructureProvider,
				Flavor:                   ptr.Deref(input.Flavor, "upgrades"),
				Namespace:                namespace.Name,
				ClusterName:              clusterName,
				KubernetesVersion:        input.E2EConfig.MustGetVariable(capi_e2e.KubernetesVersionUpgradeFrom),
				ControlPlaneMachineCount: ptr.To[int64](controlPlaneMachineCount),
				WorkerMachineCount:       ptr.To[int64](workerMachineCount),
			},
			ControlPlaneWaiters:     input.ControlPlaneWaiters,
			WaitForClusterIntervals: input.E2EConfig.GetIntervals(specName, "wait-cluster"), WaitForControlPlaneIntervals: input.E2EConfig.GetIntervals(specName, "wait-control-plane"),
			WaitForMachineDeployments: input.E2EConfig.GetIntervals(specName, "wait-worker-nodes"),
			WaitForMachinePools:       input.E2EConfig.GetIntervals(specName, "wait-machine-pool-nodes"),
		}, clusterResources)

		if clusterResources.Cluster.Spec.Topology != nil {
			// Cluster is using ClusterClass, upgrade via topology.
			By("Upgrading the Cluster topology")
			framework.UpgradeClusterTopologyAndWaitForUpgrade(ctx, framework.UpgradeClusterTopologyAndWaitForUpgradeInput{
				ClusterProxy:                   input.BootstrapClusterProxy,
				Cluster:                        clusterResources.Cluster,
				ControlPlane:                   clusterResources.ControlPlane,
				EtcdImageTag:                   etcdVersionUpgradeTo,
				DNSImageTag:                    coreDNSVersionUpgradeTo,
				MachineDeployments:             clusterResources.MachineDeployments,
				MachinePools:                   clusterResources.MachinePools,
				KubernetesUpgradeVersion:       input.E2EConfig.MustGetVariable(capi_e2e.KubernetesVersionUpgradeTo),
				WaitForMachinesToBeUpgraded:    input.E2EConfig.GetIntervals(specName, "wait-machine-upgrade"),
				WaitForMachinePoolToBeUpgraded: input.E2EConfig.GetIntervals(specName, "wait-machine-pool-upgrade"),
				WaitForKubeProxyUpgrade:        input.E2EConfig.GetIntervals(specName, "wait-machine-upgrade"),
				WaitForDNSUpgrade:              input.E2EConfig.GetIntervals(specName, "wait-machine-upgrade"),
				WaitForEtcdUpgrade:             input.E2EConfig.GetIntervals(specName, "wait-machine-upgrade"),
				PreWaitForControlPlaneToBeUpgraded: func() {
					if input.PreWaitForControlPlaneToBeUpgraded != nil {
						input.PreWaitForControlPlaneToBeUpgraded(input.BootstrapClusterProxy, namespace.Name, clusterName)
					}
				},
			})
		} else {
			// Cluster is not using ClusterClass, upgrade via individual resources.
			By("Upgrading the Kubernetes control-plane")
			var (
				upgradeCPMachineTemplateTo      *string
				upgradeWorkersMachineTemplateTo *string
			)

			if input.E2EConfig.HasVariable(capi_e2e.CPMachineTemplateUpgradeTo) {
				upgradeCPMachineTemplateTo = ptr.To(input.E2EConfig.MustGetVariable(capi_e2e.CPMachineTemplateUpgradeTo))
			}

			if input.E2EConfig.HasVariable(capi_e2e.WorkersMachineTemplateUpgradeTo) {
				upgradeWorkersMachineTemplateTo = ptr.To(input.E2EConfig.MustGetVariable(capi_e2e.WorkersMachineTemplateUpgradeTo))
			}

			framework.UpgradeControlPlaneAndWaitForUpgrade(ctx, framework.UpgradeControlPlaneAndWaitForUpgradeInput{
				ClusterProxy:                input.BootstrapClusterProxy,
				Cluster:                     clusterResources.Cluster,
				ControlPlane:                clusterResources.ControlPlane,
				EtcdImageTag:                etcdVersionUpgradeTo,
				DNSImageTag:                 coreDNSVersionUpgradeTo,
				KubernetesUpgradeVersion:    input.E2EConfig.MustGetVariable(capi_e2e.KubernetesVersionUpgradeTo),
				UpgradeMachineTemplate:      upgradeCPMachineTemplateTo,
				WaitForMachinesToBeUpgraded: input.E2EConfig.GetIntervals(specName, "wait-machine-upgrade"),
				WaitForKubeProxyUpgrade:     input.E2EConfig.GetIntervals(specName, "wait-machine-upgrade"),
				WaitForDNSUpgrade:           input.E2EConfig.GetIntervals(specName, "wait-machine-upgrade"),
				WaitForEtcdUpgrade:          input.E2EConfig.GetIntervals(specName, "wait-machine-upgrade"),
				PreWaitForControlPlaneToBeUpgraded: func() {
					if input.PreWaitForControlPlaneToBeUpgraded != nil {
						input.PreWaitForControlPlaneToBeUpgraded(input.BootstrapClusterProxy, namespace.Name, clusterName)
					}
				},
			})

			if workerMachineCount > 0 {
				By("Upgrading the machine deployment")
				framework.UpgradeMachineDeploymentsAndWait(ctx, framework.UpgradeMachineDeploymentsAndWaitInput{
					ClusterProxy:                input.BootstrapClusterProxy,
					Cluster:                     clusterResources.Cluster,
					UpgradeVersion:              input.E2EConfig.MustGetVariable(capi_e2e.KubernetesVersionUpgradeTo),
					UpgradeMachineTemplate:      upgradeWorkersMachineTemplateTo,
					MachineDeployments:          clusterResources.MachineDeployments,
					WaitForMachinesToBeUpgraded: input.E2EConfig.GetIntervals(specName, "wait-worker-nodes"),
				})

				if len(clusterResources.MachinePools) > 0 {
					By("Upgrading the machinepool instances")
					framework.UpgradeMachinePoolAndWait(ctx, framework.UpgradeMachinePoolAndWaitInput{
						ClusterProxy:                   input.BootstrapClusterProxy,
						Cluster:                        clusterResources.Cluster,
						UpgradeVersion:                 input.E2EConfig.MustGetVariable(capi_e2e.KubernetesVersionUpgradeTo),
						WaitForMachinePoolToBeUpgraded: input.E2EConfig.GetIntervals(specName, "wait-machine-pool-upgrade"),
						MachinePools:                   clusterResources.MachinePools,
					})
				}
			}
		}

		By("Waiting until nodes are ready")
		workloadProxy := input.BootstrapClusterProxy.GetWorkloadCluster(ctx, namespace.Name, clusterResources.Cluster.Name)
		workloadClient := workloadProxy.GetClient()
		framework.WaitForNodesReady(ctx, framework.WaitForNodesReadyInput{
			Lister:            workloadClient,
			KubernetesVersion: input.E2EConfig.MustGetVariable(capi_e2e.KubernetesVersionUpgradeTo),
			Count:             int(clusterResources.ExpectedTotalNodes()),
			WaitForNodesReady: input.E2EConfig.GetIntervals(specName, "wait-nodes-ready"),
		})

		if !input.SkipConformanceTests {
			By("Running conformance tests")
			// Start running the conformance test suite.
			err := kubetest.Run(
				ctx,
				kubetest.RunInput{
					ClusterProxy:       workloadProxy,
					NumberOfNodes:      int(clusterResources.ExpectedWorkerNodes()),
					ArtifactsDirectory: input.ArtifactFolder,
					ConfigFilePath:     kubetestConfigFilePath,
					GinkgoNodes:        int(clusterResources.ExpectedWorkerNodes()),
					ClusterName:        clusterResources.Cluster.GetName(),
				},
			)
			Expect(err).ToNot(HaveOccurred(), "Failed to run Kubernetes conformance")
		}

		By("PASSED!")
	})

	AfterEach(func() {
		// Dumps all the resources in the spec Namespace, then cleanups the cluster object and the spec Namespace itself.
		framework.DumpSpecResourcesAndCleanup(ctx, specName, input.BootstrapClusterProxy, input.ClusterctlConfigPath, input.ArtifactFolder, namespace, cancelWatches, clusterResources.Cluster, input.E2EConfig.GetIntervals, input.SkipCleanup)
	})
}
