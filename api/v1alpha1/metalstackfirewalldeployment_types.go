package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	MetalStackFirewallDeploymentResourceKind = "MetalStackFirewallDeployment"
	// FirewallDeploymentFinalizer allows to clean up resources associated with before removing it from the apiserver.
	FirewallDeploymentFinalizer = "metal-stack.infrastructure.cluster.x-k8s.io/firewall-deployment"

	TagFirewallDeploymentResource = "metal-stack.infrastructure.cluster.x-k8s.io/firewall-deployment-resource"

	ClusterFirewallDeploymentEnsured clusterv1.ConditionType = "ClusterFirewallDeploymentEnsured"
)

// MetalStackFirewallDeploymentSpec defines the desired state of MetalStackFirewallDeployment
type MetalStackFirewallDeploymentSpec struct {
	// FirewallTemplateRef references the MetalStackFirewallTemplate to use for the firewall deployment.
	FirewallTemplateRef *MetalStackFirewallTemplateRef `json:"firewallTemplateRef,omitempty"`
	// ManagedResourceRef references the MetalStackManagedResource that provides the underlying infrastructure for the firewall deployment.
	// +optional
	ManagedResourceRef *MetalStackManagedResourceRef `json:"managedResourceRef,omitempty"`

	// AutoUpdate defines the behavior for automatic updates.
	AutoUpdate *MetalStackFirewallAutoUpdate `json:"autoUpdate,omitempty"`
}

// MetalStackFirewallAutoUpdate defines the auto update settings for the firewall deployment.
type MetalStackFirewallAutoUpdate struct {
	// MachineImage auto updates the os image of the firewall within the maintenance time window
	// in case a newer version of the os is available.
	MachineImage bool `json:"machineImage,omitempty"`
}

// MetalStackManagedResourceRef references a MetalStackManagedResource.
type MetalStackManagedResourceRef struct {
	// Name of the MetalStackManagedResource.
	Name string `json:"name"`
}

// MetalStackFirewallTemplateRef references a MetalStackFirewallTemplate.
type MetalStackFirewallTemplateRef struct {
	// Name of the MetalStackFirewallTemplate.
	Name string `json:"name"`
}

// MetalStackFirewallDeploymentStatus defines the observed state of MetalStackFirewallDeployment
type MetalStackFirewallDeploymentStatus struct {
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// Conditions defines current service state of the MetalStackCluster.
	// +optional
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=cluster-api,shortName=msfwdeploy
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".metadata.labels.cluster\\.x-k8s\\.io/cluster-name",description="Cluster to which this MetalStackCluster belongs"
// +kubebuilder:printcolumn:name="Template",type="string",JSONPath=".spec.firewallTemplateRef.name",description="Name of the MetalStackFirewallTemplate used"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready",description="Indicates if the MetalStackFirewallDeployment is ready"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Age of the MetalStackFirewallDeployment"

// MetalStackFirewallDeployment is the Schema for the MetalStackFirewallDeployments API
type MetalStackFirewallDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MetalStackFirewallDeploymentSpec   `json:"spec,omitzero"`
	Status MetalStackFirewallDeploymentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type MetalStackFirewallDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MetalStackFirewallDeployment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MetalStackFirewallDeployment{}, &MetalStackFirewallDeploymentList{})
}

func (c *MetalStackFirewallDeployment) GetConditions() clusterv1.Conditions {
	return c.Status.Conditions
}

func (c *MetalStackFirewallDeployment) SetConditions(conditions clusterv1.Conditions) {
	c.Status.Conditions = conditions
}
