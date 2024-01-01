# K8S extensions and delegation

## Model

Client authenticates with 'APIserver', which forwards the request to a service.

https://kubernetes.io/docs/tasks/extend-kubernetes/configure-aggregation-layer/

This mechanism must be enabled in the apiserver.

This is indicated by a configmap in kube-system - extension-apiserver-authentication
- client-ca-file, requestheader-client-ca-file
- requestheader-allowed-names: "aggregator" - the SAN to check
- requestheader-group,username-headers: X-Remote-User, X-Remote-Group, X-Remote-Extra-

Role named extension-apiserver-authentication-reader in the kube-system namespace grants
permission to access the configmap.

The API server acts as a proxy to your server, and does RBAC checks. 

This is very similar to Istio and K8S Ingress gateways !

Requirement of 5 sec timeout https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/apiserver-aggregation/#response-latency

APIService CRD - named like v1.apps or v1.acme.cert-manager.io (Service=local). 

One remote example is v1beta1.metrics.k8s.io - delegated to kube-system/metrics-server


## 