package frmwrk

import (
	"context"
	"fmt"
	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	"k8s.io/utils/ptr"
	"os"

	capi_e2e "sigs.k8s.io/cluster-api/test/e2e"
)

var _ = Describe("Upgrade Kubernetes Cluster Version", Label("upgrade"), func() {
	ctx := context.TODO()
	fromKubernetesVersion := os.Getenv("KUBERNETES_VERSION_UPGRADE_FROM")
	toKubernetesVersion := os.Getenv("KUBERNETES_VERSION_UPGRADE_TO")

	ec := e2eCtx.NewE2ECluster(ClusterConfig{
		SpecName:                 "cluster-upgrade",
		NamespaceName:            "cluster-upgrade",
		ClusterName:              "cluster-upgrade",
		KubernetesVersion:        fromKubernetesVersion, //TBD
		ControlPlaneMachineImage: os.Getenv("E2E_CONTROL_PLANE_MACHINE_IMAGE_PREFIX") + fromKubernetesVersion,
		ControlPlaneMachineCount: 1,
		WorkerMachineImage:       os.Getenv("E2E_WORKER_MACHINE_IMAGE_PREFIX") + fromKubernetesVersion,
		WorkerMachineCount:       1,
	})

	ec.SetupMetalStackPreconditions(ctx)
	ec.SetupNamespace(ctx)

	DeferCleanup(func() {
		ec.Dump(context.Background())
	})

	It(fmt.Sprintf("from %s to %s", fromKubernetesVersion, toKubernetesVersion), func() {
		cfg := e2eCtx.E2EConfig.DeepCopy()
		cfg.Variables["KUBERNETES_VERSION_UPGRADE_FROM"] = e2eCtx.envOrVar("KUBERNETES_VERSION_UPGRADE_FROM")
		cfg.Variables["KUBERNETES_VERSION_UPGRADE_TO"] = e2eCtx.envOrVar("KUBERNETES_VERSION_UPGRADE_TO")
		cfg.Variables["KUBETEST_CONFIGURATION"] = e2eCtx.envOrVar("KUBETEST_CONFIGURATION")

		capi_e2e.ClusterUpgradeConformanceSpec(ctx, func() capi_e2e.ClusterUpgradeConformanceSpecInput {
			return capi_e2e.ClusterUpgradeConformanceSpecInput{
				E2EConfig:                cfg,
				ClusterctlConfigPath:     e2eCtx.Environment.ClusterctlConfigPath,
				BootstrapClusterProxy:    e2eCtx.Environment.Bootstrap,
				ArtifactFolder:           e2eCtx.Environment.artifactsPath,
				SkipCleanup:              false,
				SkipConformanceTests:     true,
				ControlPlaneMachineCount: ptr.To[int64](1),
				WorkerMachineCount:       ptr.To[int64](1),
				Flavor:                   ptr.To(e2eCtx.Environment.Flavor),
			}
		})
	})
})
