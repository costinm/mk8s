# Mesh / MultiCluster K8S helpers

This repo includes examples and helpers for using K8S in a (hopefully) simpler and more 
efficient way. 

Using informers, operators, controllers and the officially recommended 
tools remains the recommended approach for most cases, and are widely documented and used.

This project is focused on a few specific use cases and optimizations:

1. Istio-like multicluster - where an app works with a set of clusters
2. Large sets of data where we don't want the entire set in memory, as is the case with informers.
3. Caching data on disk, for fast startup and recovery, and operating in pubsub-like mode.

# Wrappers for K8S client loading

The intent is to support apps that operate on multiple clusters - like Istio multicluster - but with more flexibility.


For example the 'gcp' module will load all GKE clusters in the project or hub.
More clusters can be loaded using Istio Secrets.

The default init logic is:
- load KUBECONFIG if set explicitly
- else, load ~/.kube/config  - and load ALL contexts
- else, load in-cluster

In the end, Default cluster will be set if at least one cluster is available.

Using the default cluster we can get JWT tokens and use them to access GKE and Hub to load more clusters as needed.

```go

// instead of:
restCfg, err := clientcmd.BuildConfigFromFlags(apiServerURL, kubeConfig)
client, err := kubernetes.NewForConfig(config)

// use:

k, err := k8s.New(ctx, nil)
client, err := kubernetes.NewForConfig(k.Default.RestConfig)


```

## Terms

K8S defines or re-defines many words and concepts. To avoid confusion, I like to make clear
what they mean (to me) and how they map to common concepts.

- Api-server - this is the main k8s server, containing many modules and functions. 
  - 'Config database' is one of the core functions - actually implemented by etcd or a Kine-supported real database providing a basic 'no-SQL' storage. Storage is typical, but not required, in-memory or dynamic is also valid.
  - `CRD` is an OpenAPI description of a type, or a schema. Can also be viewed as a 'table' in SQL. Typically stored in the database - but it doesn't have to.
  - 'CR' or 'resource' is a row in the table - an individual object conforming to the CRD schema.
  - Names: each CR can be identified by a namespace and a name (except cluster-scoped resources - no namespace). The FQDN/URL also includes the cluster location.
  - 'revision' is one of the most important concepts, it allows tracking changes to a resource. Each resource has the last revision - but older revisions may also be around. Normally K8S deletes the old revision - a recovery system may keep track of them, like in git.
  - `watch` - pubsub like mechanism to get real-time updates.
- ServiceAccount and RBAC: K8S wraps a config database and adds access control to each object. 
- Gateway and API server. K8S provide a proxy service to other HTTP/2 and websocket services.
- Running containers - what most people associate with K8S is actually handled by a CRI (runtime) like docker.
  This project is NOT concerned with containers, only with K8S functions as a config database and 
  API gateway.


## Using K8S rest client directly 

Informers and regular K8S 'controllers' are based on a cached replica of type.
The informers can filter the data they cache using namespace and label selectors - but the entire
object is sent to the client as json (or protobuf for core objects).

After retrieving the full CR, the informers may build additional indexes and may drop parts of the
response to reduce memory use. 

The common pattern is at startup to retrieve all the CRs of all types that are needed, and after
getting all the data start watching for changes. 



## Using generated 'clientset' directly

# Code organization

## K8S

This is the main package used to configure the K8S rest client. 

## GKE

The github.com/costinm/mk8s/gke package includes a small library to autoconfigure K8S clients
based on the GKE and Fleet APIs and using a metadata server for tokens.

`gke-gcloud-auth-plugin` and gcloud remains the best option for using GKE with `kubectl` and 
for most other purposes. 

## Apis and generated client

The repo includes a number of tools and examples. All CRD are generated into manifests/crds, 
clientset in `client/` and informers/listers in `cachedclient`.



