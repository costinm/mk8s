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

package v1

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/apiserver-runtime/pkg/builder/resource"
	"sigs.k8s.io/apiserver-runtime/pkg/builder/resource/resourcestrategy"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Echo
// +k8s:openapi-gen=true
type Echo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EchoSpec   `json:"spec,omitempty"`
	Status EchoStatus `json:"status,omitempty"`
}

// EchoList
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type EchoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Echo `json:"items"`
}

// EchoSpec defines the desired state of Echo
type EchoSpec struct {
}

var _ resource.Object = &Echo{}
var _ resourcestrategy.Validater = &Echo{}

func (in *Echo) GetObjectMeta() *metav1.ObjectMeta {
	return &in.ObjectMeta
}

func (in *Echo) NamespaceScoped() bool {
	return false
}

func (in *Echo) New() runtime.Object {
	return &Echo{}
}

func (in *Echo) NewList() runtime.Object {
	return &EchoList{}
}

func (in *Echo) GetGroupVersionResource() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "echo.costinm.github.com",
		Version:  "v1",
		Resource: "echos",
	}
}

func (in *Echo) IsStorageVersion() bool {
	return true
}

func (in *Echo) Validate(ctx context.Context) field.ErrorList {
	// TODO(user): Modify it, adding your API validation here.
	return nil
}

var _ resource.ObjectList = &EchoList{}

func (in *EchoList) GetListMeta() *metav1.ListMeta {
	return &in.ListMeta
}

// EchoStatus defines the observed state of Echo
type EchoStatus struct {
}

func (in EchoStatus) SubResourceName() string {
	return "status"
}

// Echo implements ObjectWithStatusSubResource interface.
var _ resource.ObjectWithStatusSubResource = &Echo{}

func (in *Echo) GetStatus() resource.StatusSubResource {
	return in.Status
}

// EchoStatus{} implements StatusSubResource interface.
var _ resource.StatusSubResource = &EchoStatus{}

func (in EchoStatus) CopyTo(parent resource.ObjectWithStatusSubResource) {
	parent.(*Echo).Status = in
}
