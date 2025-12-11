package capmsutil

import (
	"context"

	"github.com/metal-stack/cluster-api-provider-metal-stack/api/v1alpha1"
	pkgerrors "github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetOwnerMetalStackCluster returns the Cluster object owning the current resource.
func GetOwnerMetalStackCluster(ctx context.Context, c client.Client, obj metav1.ObjectMeta) (*v1alpha1.MetalStackCluster, error) {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Kind != v1alpha1.MetalStackClusterKind {
			continue
		}
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil {
			return nil, pkgerrors.WithStack(err)
		}
		if gv.Group == v1alpha1.GroupVersion.Group {
			return GetInfraClusterByName(ctx, c, obj.Namespace, ref.Name)
		}
	}
	return nil, nil
}

// GetInfraClusterByName finds and return a Cluster object using the specified params.
func GetInfraClusterByName(ctx context.Context, c client.Client, namespace, name string) (*v1alpha1.MetalStackCluster, error) {
	infraCluster := &v1alpha1.MetalStackCluster{}
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}

	if err := c.Get(ctx, key, infraCluster); err != nil {
		return nil, pkgerrors.Wrapf(err, "failed to get Cluster/%s", name)
	}

	return infraCluster, nil
}
