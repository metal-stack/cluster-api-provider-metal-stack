package v1alpha1

import (
	fcmv2 "github.com/metal-stack/firewall-controller-manager/api/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories=cluster-api,shortName=msfwtemplate
// +kubebuilder:printcolumn:name="Partition",type="string",JSONPath=".spec.partition",description="The Metal Stack partition where the firewall will be created"
// +kubebuilder:printcolumn:name="Project",type="string",JSONPath=".spec.project",description="The Metal Stack project where the firewall will be created"
// +kubebuilder:printcolumn:name="Image",type="string",JSONPath=".spec.image",description="The Metal Stack firewall image"
// +kubebuilder:printcolumn:name="Size",type="string",JSONPath=".spec.size",description="The Metal Stack firewall size"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="The age of the firewall template"
type MetalStackFirewallTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the firewall deployment template spec used to create firewalls for the cluster.
	Spec fcmv2.FirewallSpec `json:"spec"`
}

// +kubebuilder:object:root=true

type MetalStackFirewallTemplateList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Items             []MetalStackFirewallTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MetalStackFirewallTemplate{}, &MetalStackFirewallTemplateList{})
}
