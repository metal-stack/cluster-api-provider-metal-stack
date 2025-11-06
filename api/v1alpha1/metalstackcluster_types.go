/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capierrors "sigs.k8s.io/cluster-api/errors" //nolint:staticcheck

	"github.com/metal-stack/metal-lib/pkg/tag"
)

const (
	// ClusterFinalizer allows to clean up resources associated with before removing it from the apiserver.
	ClusterFinalizer = "metal-stack.infrastructure.cluster.x-k8s.io/cluster"

	TagInfraClusterResource = "metal-stack.infrastructure.cluster.x-k8s.io/cluster-resource"

	ClusterControlPlaneEndpointDefaultPort = 443

	ClusterControlPlaneIPEnsured = "ClusterControlPlaneIPEnsured"
)

var (
	TagControlPlanePurpose = tag.New("metal-stack.infrastructure.cluster.x-k8s.io/purpose", "control-plane")
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MetalStackClusterSpec defines the desired state of MetalStackCluster.
type MetalStackClusterSpec struct {
	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// ProjectID is the project id of the project in metal-stack in which the associated metal-stack resources are created.
	ProjectID string `json:"projectID"`

	// NodeNetworkID is the network ID in metal-stack in which the worker nodes and the firewall of the cluster are placed.
	NodeNetworkID string `json:"nodeNetworkID"`

	// ControlPlaneIP is the ip address in metal-stack on which the control plane will be exposed.
	// If this ip and the control plane endpoint are not provided, an ephemeral ip will automatically be acquired during reconcile.
	// Static ip addresses will not be deleted.
	// +optional
	ControlPlaneIP *string `json:"controlPlaneIP,omitempty"`

	// Partition is the data center partition in which the resources are created.
	Partition string `json:"partition"`
}

// APIEndpoint represents a reachable Kubernetes API endpoint.
type APIEndpoint struct {
	// Host is the hostname on which the API server is serving.
	Host string `json:"host"`

	// Port is the port on which the API server is serving.
	Port int `json:"port"`
}

// MetalStackClusterStatus defines the observed state of MetalStackCluster.
type MetalStackClusterStatus struct {
	// Ready denotes that the cluster is ready.
	// NOTE: this field is part of the Cluster API v1beta1 contract.
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// Initialization provides information about the initialization status of the MetalStackCluster.
	// +optional
	Initialization MetalStackClusterInitializationStatus `json:"initialization,omitzero"`

	// FailureReason indicates that there is a fatal problem reconciling the
	// state, and will be set to a token value suitable for
	// programmatic interpretation.
	// +optional
	FailureReason *capierrors.ClusterStatusError `json:"failureReason,omitempty"`

	// FailureMessage indicates that there is a fatal problem reconciling the
	// state, and will be set to a descriptive error message.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`

	// Conditions defines current service state of the MetalStackCluster.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// MetalStackClusterInitializationStatus defines the observed initialization status of the MetalStackCluster.
// +kubebuilder:validation:MinProperties=1
type MetalStackClusterInitializationStatus struct {
	// Provisioned indicates that the initial provisioning has been completed.
	// NOTE: this field is part of the Cluster API v1beta2 contract, and it is used to orchestrate initial Cluster provisioning.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".metadata.labels.cluster\\.x-k8s\\.io/cluster-name",description="Cluster to which this MetalStackCluster belongs"
// +kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".spec.controlPlaneEndpoint.host",description="Control plane API endpoint"
// +kubebuilder:printcolumn:name="Partition",type="string",priority=1,JSONPath=".spec.partition",description="The partition within metal-stack"
// +kubebuilder:printcolumn:name="Project",type="string",priority=1,JSONPath=".spec.projectID",description="The project within metal-stack"
// +kubebuilder:printcolumn:name="Network",type="string",priority=1,JSONPath=".spec.nodeNetworkID",description="The network within metal-stack"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready",description="MetalStackCluster is ready"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Uptime of the cluster"

// MetalStackCluster is the Schema for the metalstackclusters API.
type MetalStackCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MetalStackClusterSpec   `json:"spec,omitempty"`
	Status MetalStackClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MetalStackClusterList contains a list of MetalStackCluster.
type MetalStackClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MetalStackCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MetalStackCluster{}, &MetalStackClusterList{})
}

// GetConditions returns the list of conditions.
func (c *MetalStackCluster) GetConditions() []metav1.Condition {
	return c.Status.Conditions
}

// SetConditions will set the given conditions.
func (c *MetalStackCluster) SetConditions(conditions []metav1.Condition) {
	c.Status.Conditions = conditions
}

func (c *MetalStackCluster) GetClusterID() string {
	return fmt.Sprintf("%s.%s", c.GetNamespace(), c.GetName())
}
