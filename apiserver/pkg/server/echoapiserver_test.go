package server

import (
	"net"
	"testing"

	echooapi "github.com/costinm/mk8s/apiserver/pkg/openapi"
	"k8s.io/apiextensions-apiserver/pkg/generated/openapi"
	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	kubeopenapi "k8s.io/kube-openapi/pkg/common"

	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/rest"
)

func TestApiserver(t *testing.T) {
	cfg := genericapiserver.NewRecommendedConfig(Codecs)
	cfg.ExternalAddress = "1.2.3.4:8081"
	cfg.LoopbackClientConfig = &rest.Config{}
	l, _ := net.Listen("tcp", ":9443")
	cfg.SecureServing = &genericapiserver.SecureServingInfo{
		Listener:     l,
		DisableHTTP2: true,
	}
	cfg.SecureServing.Cert, _ = dynamiccertificates.NewDynamicServingContentFromFiles("default", "tls.crt", "tls.key")


	cfg.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(testGetOpenAPIDefinitions, openapinamer.NewDefinitionNamer(Scheme))

	ccfg := cfg.Complete()


	srv, err := New(&ccfg)
	if err != nil {
		t.Fatal(err)
	}
	stopCh := make(chan struct{})
	srv.GenericAPIServer.PrepareRun().Run(stopCh)


}
func testGetOpenAPIDefinitions(r kubeopenapi.ReferenceCallback) map[string]kubeopenapi.OpenAPIDefinition {
	m1 := openapi.GetOpenAPIDefinitions(r)
	m2 := echooapi.GetOpenAPIDefinitions(r)
	for k, v := range m2 {
		m1[k] = v
	}
	return m1
	//map[string]kubeopenapi.OpenAPIDefinition{
	//	"k8s.io/apimachinery/pkg/apis/meta/v1.Status":          {},
	//	"k8s.io/apimachinery/pkg/apis/meta/v1.APIVersions":     {},
	//	"k8s.io/apimachinery/pkg/apis/meta/v1.APIGroupList":    {},
	//	"k8s.io/apimachinery/pkg/apis/meta/v1.ObjectMeta": {},
	//	"k8s.io/apimachinery/pkg/apis/meta/v1.APIResourceList": {},
	//	"github.com/costinm/mk8s/apiserver/pkg/apis/echo/v1.Echo": {},
	//}
}

