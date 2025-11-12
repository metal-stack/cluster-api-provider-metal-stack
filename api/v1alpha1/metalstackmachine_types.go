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
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	capierrors "sigs.k8s.io/cluster-api/errors" //nolint:staticcheck
)

const (
	// MachineFinalizer allows to clean up resources associated with before removing it from the apiserver.
	MachineFinalizer = "metal-stack.infrastructure.cluster.x-k8s.io/machine"

	TagInfraMachineResource = "metal-stack.infrastructure.cluster.x-k8s.io/machine-resource"

	ProviderMachineCreated = "MachineCreated"
	ProviderMachineReady   = "MachineReady"
	ProviderMachineHealthy = "MachineHealthy"
)

// MetalStackMachineSpec defines the desired state of MetalStackMachine.
type MetalStackMachineSpec struct {
	// ProviderID points to the metal-stack machine ID.
	// +optional
	ProviderID string `json:"providerID"`

	// Image is the operating system to deploy on the machine
	Image string `json:"image"`

	// Size is the size of the machine
	Size string `json:"size"`
}

// MetalStackMachineStatus defines the observed state of MetalStackMachine.
type MetalStackMachineStatus struct {
	// Ready denotes that the machine is ready.
	// NOTE: this field is part of the Cluster API v1beta1 contract.
	// +kubebuilder:default=false
	Ready bool `json:"ready"`

	// Initialization provides information about the initialization status of the MetalStackMachine.
	// +optional
	Initialization MetalStackMachineInitializationStatus `json:"initialization,omitzero"`

	// FailureReason indicates that there is a fatal problem reconciling the
	// state, and will be set to a token value suitable for
	// programmatic interpretation.
	// +optional
	FailureReason *capierrors.MachineStatusError `json:"failureReason,omitempty"`

	// FailureMessage indicates that there is a fatal problem reconciling the
	// state, and will be set to a descriptive error message.
	// +optional
	FailureMessage *string `json:"failureMessage,omitempty"`

	// Conditions defines current service state of the MetalStackMachine.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// MachineAddresses contains all host names, external or internal IP addresses and external or internal DNS names.
	Addresses clusterv1.MachineAddresses `json:"addresses,omitempty"`
}

// MetalStackMachineInitializationStatus defines the observed initialization status of the MetalStackMachine.
// +kubebuilder:validation:MinProperties=1
type MetalStackMachineInitializationStatus struct {
	// Provisioned indicates that the initial provisioning has been completed.
	// NOTE: this field is part of the Cluster API v1beta2 contract, and it is used to orchestrate initial Cluster provisioning.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".metadata.labels.cluster\\.x-k8s\\.io/cluster-name",description="Cluster to which this MetalStackMachine belongs"
// +kubebuilder:printcolumn:name="ProviderID",type="string",JSONPath=".spec.providerID",description="ProviderID reference for the MetalStackMachine"
// +kubebuilder:printcolumn:name="Size",type="string",JSONPath=".spec.size",priority=1,description="Size of the MetalStackMachine"
// +kubebuilder:printcolumn:name="Image",type="string",JSONPath=".spec.image",description="OS image of the MetalStackMachine"
// +kubebuilder:printcolumn:name="Provisioned",type="string",JSONPath=".status.initialization.provisioned",description="MetalStackCluster is ready"
// +kubebuilder:printcolumn:name="Healthy",type="string",JSONPath=".status.conditions[1].status",description="Health of the provider machine"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// MetalStackMachine is the Schema for the metalstackmachines API.
type MetalStackMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MetalStackMachineSpec   `json:"spec,omitempty"`
	Status MetalStackMachineStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MetalStackMachineList contains a list of MetalStackMachine.
type MetalStackMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MetalStackMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MetalStackMachine{}, &MetalStackMachineList{})
}

// GetConditions returns the list of conditions.
func (c *MetalStackMachine) GetConditions() []metav1.Condition {
	return c.Status.Conditions
}

// SetConditions will set the given conditions.
func (c *MetalStackMachine) SetConditions(conditions []metav1.Condition) {
	c.Status.Conditions = conditions
}
