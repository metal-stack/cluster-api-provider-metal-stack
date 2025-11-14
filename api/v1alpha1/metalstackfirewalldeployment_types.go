package v1alpha1

type MetalStackFirewallDeploymentSpec struct {
	AutoUpdate          *MetalStackFirewallAutoUpdate  `json:"autoUpdate,omitempty"`
	FirewallTemplateRef *MetalStackFirewallTemplateRef `json:"firewallTemplateRef,omitempty"`
}

type MetalStackFirewallAutoUpdate struct {
	MachineImage bool `json:"machineImage,omitempty"`
}

type MetalStackFirewallTemplateRef struct {
	Name string `json:"name"`
}

type MetalStackFirewallDeploymentStatus struct {
	// +kubebuilder:default=false
	Ready bool `json:"ready"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=cluster-api,shortName=msfwdeploy
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".metadata.labels.cluster\\.x-k8s\\.io/cluster-name",description="Cluster to which this MetalStackCluster belongs"
// +kubebuilder:printcolumn:name="Template",type="string",JSONPath=".spec.firewallTemplateRef.name",description="Name of the MetalStackFirewallTemplate used"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready",description="Indicates if the MetalStackFirewallDeployment is ready"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Age of the MetalStackFirewallDeployment"
type MetalStackFirewallDeployment struct {
	Spec   MetalStackFirewallDeploymentSpec   `json:"spec,omitzero"`
	Status MetalStackFirewallDeploymentStatus `json:"status,omitzero"`
}
