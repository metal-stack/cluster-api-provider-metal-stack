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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// MetalStackMachineTemplateSpec defines the desired state of MetalStackMachineTemplateSpec.
type MetalStackMachineTemplateSpec struct {
	Template MetalStackMachine `json:"template"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=inframachinetemplates,scope=Namespaced,categories=cluster-api,shortName=imt
// +kubebuilder:storageversion

// MetalStackMachineTemplate is the Schema for the inframachinetemplates API.
type MetalStackMachineTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec MetalStackMachineTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// MetalStackMachineTemplateList contains a list of MetalStackMachineTemplate.
type MetalStackMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MetalStackMachineTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MetalStackMachineTemplate{}, &MetalStackMachineTemplateList{})
}

// MetalStackMachineTemplateResource describes the data needed to create a metal-stack machine from a template.
type MetalStackMachineTemplateResource struct {
	Spec MetalStackMachineSpec `json:"spec"`
}
