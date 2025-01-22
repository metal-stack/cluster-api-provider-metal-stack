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
	capierrors "sigs.k8s.io/cluster-api/errors" //nolint:staticcheck
)

const (
	// MachineFinalizer allows to clean up resources associated with before removing it from the apiserver.
	MachineFinalizer = "metal-stack.infrastructure.cluster.x-k8s.io/machine"

	TagInfraMachineID = "metal-stack.infrastructure.cluster.x-k8s.io/machine-id"

	ProviderMachineCreated clusterv1.ConditionType = "MachineCreated"
	ProviderMachineReady   clusterv1.ConditionType = "MachineReady"
	ProviderMachineHealthy clusterv1.ConditionType = "MachineHealthy"
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
	Ready bool `json:"ready"`

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
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`

	// MachineAddresses contains all host names, external or internal IP addresses and external or internal DNS names.
	Addresses clusterv1.MachineAddresses `json:"addresses,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

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
func (c *MetalStackMachine) GetConditions() clusterv1.Conditions {
	return c.Status.Conditions
}

// SetConditions will set the given conditions.
func (c *MetalStackMachine) SetConditions(conditions clusterv1.Conditions) {
	c.Status.Conditions = conditions
}
