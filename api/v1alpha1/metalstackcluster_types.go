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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	capierrors "sigs.k8s.io/cluster-api/errors"

	fcmv2 "github.com/metal-stack/firewall-controller-manager/api/v2"
)

const (
	// ClusterFinalizer allows to clean up resources associated with before removing it from the apiserver.
	ClusterFinalizer = "metal-stack.infrastructure.cluster.x-k8s.io/cluster"

	ClusterNodeNetworkEnsured      clusterv1.ConditionType = "ClusterNodeNetworkEnsured"
	ClusterFirewallDeploymentReady clusterv1.ConditionType = "ClusterFirewallDeploymentReady"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MetalStackClusterSpec defines the desired state of MetalStackCluster.
type MetalStackClusterSpec struct {
	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// ProjectID is the project id of the project in metal-stack in which the associated metal-stack resources are created
	ProjectID string `json:"projectID"`

	// Partition is the data center partition in which the resources are created
	Partition string `json:"partition"`

	// Firewall describes the firewall for this cluster
	Firewall Firewall `json:"firewall"`
}

// APIEndpoint represents a reachable Kubernetes API endpoint.
type APIEndpoint struct {
	// Host is the hostname on which the API server is serving.
	Host string `json:"host"`

	// Port is the port on which the API server is serving.
	Port int `json:"port"`
}

// Firewall defines parameters for the firewall creation along with configuration for the firewall-controller.
type Firewall struct {
	// Size is the machine size of the firewall.
	// An update on this field requires the recreation of the physical firewall and can therefore lead to traffic interruption for the cluster.
	Size string `json:"size"`
	// Image is the os image of the firewall.
	// An update on this field requires the recreation of the physical firewall and can therefore lead to traffic interruption for the cluster.
	Image string `json:"image"`
	// AdditionalNetworks are the networks to which this firewall is connected.
	// An update on this field requires the recreation of the physical firewall and can therefore lead to traffic interruption for the cluster.
	// +optional
	AdditionalNetworks []string `json:"networks,omitempty"`

	// RateLimits allows configuration of rate limit rules for interfaces.
	// +optional
	RateLimits []fcmv2.RateLimit `json:"rateLimits,omitempty"`
	// EgressRules contains egress rules configured for this firewall.
	// +optional
	EgressRules []fcmv2.EgressRuleSNAT `json:"egressRules,omitempty"`

	// LogAcceptedConnections if set to true, also log accepted connections in the droptailer log.
	// +optional
	LogAcceptedConnections *bool `json:"logAcceptedConnections,omitempty"`
}

// MetalStackClusterStatus defines the observed state of MetalStackCluster.
type MetalStackClusterStatus struct {
	// Ready denotes that the cluster is ready.
	Ready bool `json:"ready"`

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
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

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
func (c *MetalStackCluster) GetConditions() clusterv1.Conditions {
	return c.Status.Conditions
}

// SetConditions will set the given conditions.
func (c *MetalStackCluster) SetConditions(conditions clusterv1.Conditions) {
	c.Status.Conditions = conditions
}
