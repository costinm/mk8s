/*
Copyright 2016 The Kubernetes Authors.

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

package server

import (
	"context"

	echov1 "github.com/costinm/mk8s/apiserver/pkg/apis/echo/v1"
	v1 "github.com/costinm/mk8s/apiserver/pkg/apis/echo/v1"
	echooapi "github.com/costinm/mk8s/apiserver/pkg/openapi"
	"k8s.io/apiextensions-apiserver/pkg/generated/openapi"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	kubeopenapi "k8s.io/kube-openapi/pkg/common"
)

var (
	// Scheme defines methods for serializing and deserializing API objects.
	Scheme = runtime.NewScheme()

	// Codecs provides methods for retrieving codecs and serializers for specific
	// versions and content types.
	Codecs              = serializer.NewCodecFactory(Scheme)
)

func init() {
	// Add the APIs to the scheme - used to parse the incoming objects
	echov1.AddToScheme(Scheme)

	// we need to add the options to empty v1
	// TODO fix the server code to avoid this
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})

	// TODO: keep the generic API server from wanting this
	unversioned := schema.GroupVersion{Group: "", Version: "v1"}

	Scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	)
}


// EchoServer contains state for a Kubernetes cluster master/api server.
type EchoServer struct {
	GenericAPIServer *genericapiserver.GenericAPIServer
}

// New returns a new instance of EchoServer from the given config.
func  New(cfg *genericapiserver.CompletedConfig) (*EchoServer, error) {

	// There is a RecommendedConfig with a Complete() call ?
	genericServer, err := cfg.New("echo-apiserver", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}

	s := &EchoServer{
		GenericAPIServer: genericServer,
	}

	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(v1.GroupName, Scheme, metav1.ParameterCodec, Codecs)

	v1beta1storage := map[string]rest.Storage{}

	//v1beta1storage["echos"], err = NewREST(Scheme, cfg.RESTOptionsGetter)
	//if err != nil {
	//	return nil, err
	//}
	v1beta1storage["echo"] = &EchoHandler{}
	v1beta1storage["echos"] = &EchoHandler{}

	apiGroupInfo.VersionedResourcesStorageMap["v1"] = v1beta1storage

	if err := s.GenericAPIServer.InstallAPIGroup(&apiGroupInfo); err != nil {
		return nil, err
	}

	return s, nil
}

type EchoHandler struct {
	updated int
	created int
	// Implementing this makes apiserver believe we support list, etc
	// rest.StandardStorage
}

func (f *EchoHandler) Destroy() {
}

func (f *EchoHandler) New() runtime.Object {
	return &echov1.Echo{}
}

// Required
func (f *EchoHandler) NamespaceScoped() bool {
	return false
}

func (f *EchoHandler) GetSingularName() string {
	return "echo"
}

func (f *EchoHandler) Create(ctx context.Context, obj runtime.Object, createValidation rest.ValidateObjectFunc, options *metav1.CreateOptions) (runtime.Object, error) {
	f.created++
	return obj, nil
}

func (f *EchoHandler) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, forceAllowCreate bool, options *metav1.UpdateOptions) (runtime.Object, bool, error) {
	obj, err := objInfo.UpdatedObject(ctx, &echov1.Echo{})
	if err != nil {
		return obj, false, err
	}
	f.updated++
	return nil, false, nil
}


func (f *EchoHandler) Get(ctx context.Context, id string, options *metav1.GetOptions) (runtime.Object, error) {
	return &echov1.Echo {
		TypeMeta:   metav1.TypeMeta{
		Kind: "echo",
		APIVersion: echov1.GroupVersion.String(),
	},
		ObjectMeta: metav1.ObjectMeta{
			Name: id,
		},
		Spec:       v1.EchoSpec{
			Msg: "Hello " + id + " " + options.String(),
		},
		Status:     v1.EchoStatus{},
	}, nil
}

func (f *EchoHandler) NewList() runtime.Object {
	return &echov1.EchoList{}
}

func (f *EchoHandler) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	return &echov1.EchoList{
		Items: []echov1.Echo{
			{	TypeMeta:   metav1.TypeMeta{
				Kind: "echo",
				APIVersion: echov1.GroupVersion.String(),
			},
				ObjectMeta: metav1.ObjectMeta{
				Name: "foo",
				},
				Spec:       v1.EchoSpec{			Msg: "Hello " + options.String()},
				Status:     v1.EchoStatus{},
			},
		},
	}, nil
}

func (f *EchoHandler) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	return nil, nil
}


func GetOpenAPIDefinitions(r kubeopenapi.ReferenceCallback) map[string]kubeopenapi.OpenAPIDefinition {
	m1 := openapi.GetOpenAPIDefinitions(r)
	m2 := echooapi.GetOpenAPIDefinitions(r)
	for k, v := range m2 {
		m1[k] = v
	}
	return m1
}

