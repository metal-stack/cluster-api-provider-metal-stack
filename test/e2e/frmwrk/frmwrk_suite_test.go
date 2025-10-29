package frmwrk

import (
	"context"
	"testing"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	e2eCtx *E2EContext
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)

	ctrl.SetLogger(klog.Background())
	ginkgo.RunSpecs(t, "capms-e2e")
}

var _ = ginkgo.BeforeSuite(func() {
	e2eCtx = NewE2EContext()
	e2eCtx.ProvideBootstrapCluster()
	e2eCtx.CreateClusterctlConfig(context.TODO())
	e2eCtx.InitManagementCluster(context.TODO())
})

var _ = ginkgo.AfterSuite(func() {
	if ginkgo.CurrentSpecReport().Failed() {
		// on failure, we skip cleanup to investigate
		return
	}
	if e2eCtx != nil {
		e2eCtx.Teardown(context.TODO())
	}
})
