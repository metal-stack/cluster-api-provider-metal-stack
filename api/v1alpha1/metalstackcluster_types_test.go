package v1alpha1_test

import (
	"github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

var _ = Describe("MetalStackCluster", func() {
	It("GetClusterID is a valid label value", func() {
		cluster := &v1alpha1.MetalStackCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "some-namespace",
			},
		}

		clusterID := cluster.GetClusterName()
		Expect(utilvalidation.IsValidLabelValue(clusterID)).To(BeEmpty())
	})

	It("GetClusterID is constant", func() {
		cluster := &v1alpha1.MetalStackCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "some-namespace",
			},
		}

		clusterID := cluster.GetClusterName()
		Expect(clusterID).To(Equal("some-namespace.my-cluster"))
	})
})
