/*
Copyright 2020 The Kubernetes Authors.

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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:categories=mesh-api
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="IP",type=string,JSONPath=`.spec.ip`
// +kubebuilder:printcolumn:name="Host",type=string,JSONPath=`.status.host`

// Ptr represents a pointer from an IP address to host info.
type Ptr struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the state of a destination - it is dynamically determined by
	// different mechanisms.
	//
	Spec PtrSpec `json:"spec"`

	// Status defines the current state of a destination.
	//
	// +kubebuilder:default={conditions: {{type: "Accepted", status: "Unknown", reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type: "Programmed", status: "Unknown", reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status PtrStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PtrList contains a list of Ptrs.
type PtrList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Ptr `json:"items"`
}

// The actual objects used in the server.

// PtrSpec defines the desired state of Ptr.
type PtrSpec struct {
	// Support: Core
	Host string `json:"host"`
	IP   string `json:"ip"`
}

type PtrStatus struct {
	Host string `json:"host"`
	IP   string `json:"ip"`
}
