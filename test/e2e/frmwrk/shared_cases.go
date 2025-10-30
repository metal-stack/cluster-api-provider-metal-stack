package frmwrk

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	. "github.com/onsi/gomega"    //nolint:staticcheck

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/cluster-api/util"

	"github.com/drone/envsubst/v2"
)

type ClusterUpgradeSpecInput struct {
	E2EConfig                *clusterctl.E2EConfig
	ClusterctlConfigPath     string
	BootstrapClusterProxy    framework.ClusterProxy
	ArtifactFolder           string
	SkipCleanup              bool
	SkipConformanceTests     bool
	ControlPlaneWaiters      clusterctl.ControlPlaneWaiters
	InfrastructureProvider   *string
	ControlPlaneMachineCount *int64
	WorkerMachineCount       *int64
}

func createE2ECluster(ctx context.Context, e2eCtx *E2EContext, cfg ClusterConfig) *E2ECluster {
	ec := e2eCtx.NewE2ECluster(cfg)

	ec.SetupMetalStackPreconditions(ctx)
	ec.SetupNamespace(ctx)
	ec.GenerateAndApplyClusterTemplate(ctx)

	DeferCleanup(func() {
		ec.Dump(context.Background())
	})

	By("Wait for cluster")
	framework.DiscoveryAndWaitForCluster(ctx, framework.DiscoveryAndWaitForClusterInput{
		Getter:    e2eCtx.Environment.Bootstrap.GetClient(),
		Namespace: ec.Refs.Cluster.Namespace,
		Name:      ec.Refs.Cluster.Name,
	}, e2eCtx.E2EConfig.GetIntervals("default", "wait-cluster")...)

	controlPlane := framework.GetKubeadmControlPlaneByCluster(ctx, framework.GetKubeadmControlPlaneByClusterInput{
		Lister:      e2eCtx.Environment.Bootstrap.GetClient(),
		ClusterName: ec.Refs.Cluster.Name,
		Namespace:   ec.Refs.Cluster.Namespace,
	})
	Expect(controlPlane).To(Not(BeNil()))

	By("Wait for kubeadm control plane")
	framework.DiscoveryAndWaitForControlPlaneInitialized(ctx, framework.DiscoveryAndWaitForControlPlaneInitializedInput{
		Lister:  e2eCtx.Environment.Bootstrap.GetClient(),
		Cluster: ec.Refs.Cluster,
	}, e2eCtx.E2EConfig.GetIntervals("default", "wait-control-plane")...)

	framework.WaitForClusterToProvision(ctx, framework.WaitForClusterToProvisionInput{
		Cluster: ec.Refs.Cluster,
		Getter:  e2eCtx.Environment.Bootstrap.GetClient(),
	}, e2eCtx.E2EConfig.GetIntervals("default", "wait-cluster-provisioned")...)

	framework.WaitForOneKubeadmControlPlaneMachineToExist(ctx, framework.WaitForOneKubeadmControlPlaneMachineToExistInput{
		Lister:       e2eCtx.Environment.Bootstrap.GetClient(),
		Cluster:      ec.Refs.Cluster,
		ControlPlane: controlPlane,
	}, e2eCtx.E2EConfig.GetIntervals("default", "wait-control-plane-machine")...)

	framework.WaitForControlPlaneAndMachinesReady(ctx, framework.WaitForControlPlaneAndMachinesReadyInput{
		Cluster:      ec.Refs.Cluster,
		GetLister:    e2eCtx.Environment.Bootstrap.GetClient(),
		ControlPlane: controlPlane,
	}, e2eCtx.E2EConfig.GetIntervals("default", "wait-control-plane-and-machines-ready")...)

	return ec
}

// Reference: https://github.com/kubernetes-sigs/cluster-api/blob/76697d5bf2a746aa1bf942c88170692666bfcc5c/test/e2e/cluster_upgrade.go#L37
func clusterUpgrade(ctx context.Context, e2eCtx *E2EContext, inputGetter func() ClusterUpgradeSpecInput) {

	var (
		specName  = "cluster-upgrade"
		input     ClusterUpgradeSpecInput
		namespace *corev1.Namespace
		//cancelWatches context.CancelFunc

		KubernetesVersionUpgradeFrom    = "KUBERNETES_VERSION_UPGRADE_FROM"
		KubernetesVersionUpgradeTo      = "KUBERNETES_VERSION_UPGRADE_TO"
		CPMachineTemplateUpgradeTo      = "CONTROL_PLANE_MACHINE_TEMPLATE_UPGRADE_TO"
		WorkersMachineTemplateUpgradeTo = "WORKERS_MACHINE_TEMPLATE_UPGRADE_TO"

		controlPlaneMachineCount int64
		workerMachineCount       int64

		clusterResources *clusterctl.ApplyClusterTemplateAndWaitResult

		clusterName string
	)

	ec := e2eCtx.NewE2ECluster(ClusterConfig{
		SpecName:      specName,
		NamespaceName: "cluster-upgrade",
		ClusterName:   "e2e-upgrade",
	})
	ec.SetupMetalStackPreconditions(ctx)
	//ec.SetupNamespace(ctx)

	BeforeEach(func() {
		Expect(ctx).NotTo(BeNil(), "ctx is required for %s spec", specName)
		input = inputGetter()
		Expect(input.E2EConfig).ToNot(BeNil(), "Invalid argument. input.E2EConfig can't be nil when calling %s spec", specName)
		Expect(input.ClusterctlConfigPath).To(BeAnExistingFile(), "Invalid argument. input.ClusterctlConfigPath must be an existing file when calling %s spec", specName)
		Expect(input.BootstrapClusterProxy).ToNot(BeNil(), "Invalid argument. input.BootstrapClusterProxy can't be nil when calling %s spec", specName)
		Expect(os.MkdirAll(input.ArtifactFolder, 0750)).To(Succeed(), "Invalid argument. input.ArtifactFolder can't be created for %s spec", specName)

		Expect(input.E2EConfig.Variables).To(HaveKey(KubernetesVersionUpgradeFrom))
		Expect(input.E2EConfig.Variables).To(HaveKey(KubernetesVersionUpgradeTo))

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

		clusterResources = new(clusterctl.ApplyClusterTemplateAndWaitResult)
	})

	It("Should create and upgrade a workload cluster", func() {
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
				KubeconfigPath:           input.BootstrapClusterProxy.GetKubeconfigPath(),
				InfrastructureProvider:   infrastructureProvider,
				Namespace:                namespace.Name,
				ClusterName:              clusterName,
				KubernetesVersion:        input.E2EConfig.MustGetVariable(KubernetesVersionUpgradeFrom),
				ControlPlaneMachineCount: ptr.To[int64](controlPlaneMachineCount),
				WorkerMachineCount:       ptr.To[int64](workerMachineCount),
			},
			ControlPlaneWaiters:          input.ControlPlaneWaiters,
			WaitForClusterIntervals:      input.E2EConfig.GetIntervals(specName, "wait-cluster"),
			WaitForControlPlaneIntervals: input.E2EConfig.GetIntervals(specName, "wait-control-plane"),
			WaitForMachineDeployments:    input.E2EConfig.GetIntervals(specName, "wait-worker-nodes"),
			WaitForMachinePools:          input.E2EConfig.GetIntervals(specName, "wait-machine-pool-nodes"),
		}, clusterResources)

		By("Wait for CNI and CCM")
		targetTemplate, err := os.ReadFile(path.Join(e2eCtx.Environment.artifactsPath, "config", "target", "base.yaml"))
		Expect(err).ToNot(HaveOccurred())

		vars := ec.Variables()
		targetResources, err := envsubst.Eval(string(targetTemplate), func(varName string) string {
			return vars[varName]
		})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() error {
			return ec.Refs.Workload.CreateOrUpdate(ctx, []byte(targetResources))
		}, "10m", "15s").Should(Succeed()) // currently this long delay is required as machines might not be ready yet

		By("Upgrading the Kubernetes control-plane")
		var (
			upgradeCPMachineTemplateTo      *string
			upgradeWorkersMachineTemplateTo *string
		)

		if input.E2EConfig.HasVariable(CPMachineTemplateUpgradeTo) {
			upgradeCPMachineTemplateTo = ptr.To(input.E2EConfig.MustGetVariable(CPMachineTemplateUpgradeTo))
		}

		if input.E2EConfig.HasVariable(WorkersMachineTemplateUpgradeTo) {
			upgradeWorkersMachineTemplateTo = ptr.To(input.E2EConfig.MustGetVariable(WorkersMachineTemplateUpgradeTo))
		}

		framework.UpgradeControlPlaneAndWaitForUpgrade(ctx, framework.UpgradeControlPlaneAndWaitForUpgradeInput{
			ClusterProxy:                       input.BootstrapClusterProxy,
			Cluster:                            clusterResources.Cluster,
			ControlPlane:                       clusterResources.ControlPlane,
			EtcdImageTag:                       "",
			DNSImageTag:                        "",
			KubernetesUpgradeVersion:           input.E2EConfig.MustGetVariable(KubernetesVersionUpgradeTo),
			UpgradeMachineTemplate:             upgradeCPMachineTemplateTo,
			WaitForMachinesToBeUpgraded:        input.E2EConfig.GetIntervals(specName, "wait-machine-upgrade"),
			WaitForKubeProxyUpgrade:            input.E2EConfig.GetIntervals(specName, "wait-machine-upgrade"),
			WaitForDNSUpgrade:                  input.E2EConfig.GetIntervals(specName, "wait-machine-upgrade"),
			WaitForEtcdUpgrade:                 input.E2EConfig.GetIntervals(specName, "wait-machine-upgrade"),
			PreWaitForControlPlaneToBeUpgraded: func() {},
		})

		if workerMachineCount > 0 {
			By("Upgrading the machine deployment")
			framework.UpgradeMachineDeploymentsAndWait(ctx, framework.UpgradeMachineDeploymentsAndWaitInput{
				ClusterProxy:                input.BootstrapClusterProxy,
				Cluster:                     clusterResources.Cluster,
				UpgradeVersion:              input.E2EConfig.MustGetVariable(KubernetesVersionUpgradeTo),
				UpgradeMachineTemplate:      upgradeWorkersMachineTemplateTo,
				MachineDeployments:          clusterResources.MachineDeployments,
				WaitForMachinesToBeUpgraded: input.E2EConfig.GetIntervals(specName, "wait-worker-nodes"),
			})

			if len(clusterResources.MachinePools) > 0 {
				By("Upgrading the machinepool instances")
				framework.UpgradeMachinePoolAndWait(ctx, framework.UpgradeMachinePoolAndWaitInput{
					ClusterProxy:                   input.BootstrapClusterProxy,
					Cluster:                        clusterResources.Cluster,
					UpgradeVersion:                 input.E2EConfig.MustGetVariable(KubernetesVersionUpgradeTo),
					WaitForMachinePoolToBeUpgraded: input.E2EConfig.GetIntervals(specName, "wait-machine-pool-upgrade"),
					MachinePools:                   clusterResources.MachinePools,
				})
			}
		}

		By("Waiting until nodes are ready")
		workloadProxy := input.BootstrapClusterProxy.GetWorkloadCluster(ctx, namespace.Name, clusterResources.Cluster.Name)
		workloadClient := workloadProxy.GetClient()
		framework.WaitForNodesReady(ctx, framework.WaitForNodesReadyInput{
			Lister:            workloadClient,
			KubernetesVersion: input.E2EConfig.MustGetVariable(KubernetesVersionUpgradeTo),
			Count:             int(clusterResources.ExpectedTotalNodes()),
			WaitForNodesReady: input.E2EConfig.GetIntervals(specName, "wait-nodes-ready"),
		})

		By("PASSED!")
	})

	//AfterEach(func() {
	//	// Dumps all the resources in the spec Namespace, then cleanups the cluster object and the spec Namespace itself.
	//	framework.DumpSpecResourcesAndCleanup(ctx, specName, input.BootstrapClusterProxy, input.ClusterctlConfigPath, input.ArtifactFolder, namespace, cancelWatches, clusterResources.Cluster, input.E2EConfig.GetIntervals, input.SkipCleanup)
	//})
}
