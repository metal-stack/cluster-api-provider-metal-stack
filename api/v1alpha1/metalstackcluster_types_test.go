package v1alpha1_test

import (
	"github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("MetalStackCluster", func() {
	It("GetClusterID is a valid label value", func() {
		cluster := &v1alpha1.MetalStackCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "some-namespace",
			},
		}

		clusterID := cluster.GetClusterID()
		// We extracted this regexp from an error message when trying to set a wrong label
		Expect(clusterID).To(MatchRegexp("^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$"))
	})

	It("GetClusterID is constant", func() {
		cluster := &v1alpha1.MetalStackCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-cluster",
				Namespace: "some-namespace",
			},
		}

		clusterID := cluster.GetClusterID()
		Expect(clusterID).To(Equal("some-namespace.my-cluster"))
	})
})
