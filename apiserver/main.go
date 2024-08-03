package main

import (
	"log"
	"net"

	"github.com/costinm/mk8s/apiserver/pkg/server"
	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	"k8s.io/client-go/rest"
)

func main() {
	cfg := genericapiserver.NewRecommendedConfig(server.Codecs)
	cfg.ExternalAddress = "1.2.3.4:8081"
	cfg.LoopbackClientConfig = &rest.Config{}
	l, _ := net.Listen("tcp", ":9443")
	cfg.SecureServing = &genericapiserver.SecureServingInfo{
		Listener:     l,
		DisableHTTP2: true,
	}
	cfg.SecureServing.Cert, _ = dynamiccertificates.NewDynamicServingContentFromFiles("default", "tls.crt", "tls.key")


	cfg.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(server.GetOpenAPIDefinitions, openapinamer.NewDefinitionNamer(server.Scheme))

	ccfg := cfg.Complete()


	srv, err := server.New(&ccfg)
	if err != nil {
		log.Fatal(err)
	}
	stopCh := make(chan struct{})
	srv.GenericAPIServer.PrepareRun().Run(stopCh)


}
