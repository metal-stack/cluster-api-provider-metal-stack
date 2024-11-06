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
)

const (
	// MachineFinalizer allows to clean up resources associated with before removing it from the apiserver.
	MachineFinalizer = "metal-stack.infrastructure.cluster.x-k8s.io/machine"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MetalStackMachineSpec defines the desired state of MetalStackMachine.
type MetalStackMachineSpec struct {
	// Image is the operating system to deploy on the machine
	Image string `json:"image"`

	// Size is the size of the machine
	Size string `json:"size"`
}

// MetalStackMachineStatus defines the observed state of MetalStackMachine.
type MetalStackMachineStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
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
