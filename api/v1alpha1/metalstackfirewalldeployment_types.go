package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	MetalStackFirewallDeploymentKind = "MetalStackFirewallDeployment"
	// FirewallDeploymentFinalizer allows to clean up resources associated with before removing it from the apiserver.
	FirewallDeploymentFinalizer = "metal-stack.infrastructure.cluster.x-k8s.io/firewall-deployment"

	TagFirewallDeploymentResource = "metal-stack.infrastructure.cluster.x-k8s.io/firewall-deployment-resource"

	ClusterFirewallDeploymentEnsured = "ClusterFirewallDeploymentEnsured"
)

// MetalStackFirewallDeploymentSpec defines the desired state of MetalStackFirewallDeployment
type MetalStackFirewallDeploymentSpec struct {
	// FirewallTemplateRef references the MetalStackFirewallTemplate to use for the firewall deployment.
	FirewallTemplateRef MetalStackFirewallTemplateRef `json:"firewallTemplateRef"`
	// ManagedResourceRef references the resource that represents the firewall deployment. At this moment it is a metal-stack firewall id.
	// It is planned to reference a FirewallDeployment.
	// +optional
	ManagedResourceRef *MetalStackManagedResourceRef `json:"managedResourceRef,omitempty"`

	// AutoUpdate defines the behavior for automatic updates.
	AutoUpdate *MetalStackFirewallAutoUpdate `json:"autoUpdate,omitempty"`
}

type MetalStackFirewallAutoUpdate struct {
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
	// NOTE: this field is part of the Cluster API v1beta1 contract.
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// Initialization provides information about the initialization status of the MetalStackCluster.
	// +optional
	Initialization MetalStackClusterInitializationStatus `json:"initialization,omitzero"`

	// Conditions defines current service state of the MetalStackCluster.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// MetalStackFirewallDeploymentInitializationStatus defines the observed initialization status of the MetalStackFirewallDeployment.
// +kubebuilder:validation:MinProperties=1
type MetalStackFirewallDeploymentInitializationStatus struct {
	// Provisioned indicates that the initial provisioning has been completed.
	// NOTE: this field is part of the FirewallDeployment API v1beta2 contract, and it is used to orchestrate initial FirewallDeployment provisioning.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=cluster-api,shortName=msfwdeploy
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".metadata.labels.cluster\\.x-k8s\\.io/cluster-name",description="Cluster to which this MetalStackCluster belongs"
// +kubebuilder:printcolumn:name="Template",type="string",JSONPath=".spec.firewallTemplateRef.name",description="Name of the MetalStackFirewallTemplate used"
// +kubebuilder:printcolumn:name="Deployment",type="string",JSONPath=".spec.managedResourceRef.name",description="Name of the managed FirewallDeployment"
// +kubebuilder:printcolumn:name="Provisioned",type="string",JSONPath=".status.initialization.provisioned",description="Indicates if the MetalStackFirewallDeployment has been initialized"
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

func (c *MetalStackFirewallDeployment) GetConditions() []metav1.Condition {
	return c.Status.Conditions
}

func (c *MetalStackFirewallDeployment) SetConditions(conditions []metav1.Condition) {
	c.Status.Conditions = conditions
}
