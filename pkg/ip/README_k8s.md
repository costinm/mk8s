# K8S notes

The meshauth module is focused on authentication in K8S and mesh environments.
An important mechanism is 'same node' verification and 'IP to pod' mappings.

I am trying to minimize external dependencies - this package used K8S API
and should be used if K8S is used in an app.

There are few pieces:
- initializing the K8S client. A lighter alternative without any dependency
but lower capabilities is in the parent package, intended for use in WASM or
very low footprint.
- getting tokens, secrets and config maps from K8S
- low overhead interface for 'list and watch' - allowing pod watching but also other config 
objects

## K8S Informers

TL;DR:
- probably safest option for common use cases
- a lot of memory - holds all objects in a cache, must use filtering
- *don't use ResourceEventHandlerFuncs* - implement the interface, and use 'isInInitialList'.

The most common way to interact with K8S is using informers - client-go/tools/cache. 

A 'shared informer' will get all the objects of a certain kind, with some
query string and invoke a method on changes. The client-go package is mostly build around this - listers/core/v1/..
and all generated classes use the cache.

It holds ALL objects in memory - but it may drop some fields after getting the JSON from K8S, and allows optimizations
if different parts of the code list or watch the same resource.

Few things worth knowing:
- core objects are usually available as proto or json. CRDs as json only.
- when working with CRDs - the 'dynamic informer' is used.
  See [istio comments](https://github.com/istio/istio/blob/master/pilot/pkg/config/kube/crdclient/client.go) on
  who it's faster to use generated informer, as the low level dynamic informer stores json and requires 
  frequent conversions
- Istio has quite a lot of good code around informer reflecting a long history.

I think informers are great for many apps using K8S extensively, like Istio. 
I don't think they're best for either occasional use (light) or optimal use.

As implementation, the basis of the cache is a ListerWatcher - with metav1.ListOptions to filter.
The watch lives in apimachinery - which is independent of the client-go ( handling transport, informers,
caches, etc). `listwatch.go` is using restclient.Request.Watch() method.

The code is:

```golang
           c.Get().
			Namespace(namespace).
			Resource(resource).
			VersionedParams(&options, metav1.ParameterCodec).
followed by:		
			Do(ctx).Get()
or 
            Watch(ctx)
```

`retrywatcher.go` and `reflector.go` ListAndWatch are worth reading.
'UseWatchList' is the new streaming way - instead of paging List (ENABLE_CLIENT_GO_WATCH_LIST_ALPHA)
https://github.com/kubernetes/enhancements/tree/master/keps/sig-api-machinery/3157-watch-list#proposal



The client-go library includes auto-generated files:
- listers - using the shared informers.
- discovery - query the server for CRDs, cached
- dynamic - less efficient CRD support
- kubernetes - the 'client set'

Relatively separate:
- rest - the real interface, including watch
- tools/clientcmd - loading kubeconfig files to create transport and rest.
- plugin - native gcp, oidc, azure and exec auth
- transport - round trippers, Config - doesn't depend on the rest, handles all auth and TLS details
- rate control, debug

The apimachinery package is interesting - it has the low level parsing for streams, etc - but no transport and is 
documented as 'no API guarantees' ( unlike client )

The k8s.io/api has the generated types - json style. Depends on apimachinery (and gogo protobuf !).

## Casual use

If you just want to get a config map - and be notified when it changes - an 
informer is overkill. Same is true if you want to optimize memory use - and don't need the full cache.

You can either make a REST request - K8S is a standard HTTP server and very easy
to use if you have token/cert. Or use the rest and transport directly.

## Non-cached use

### Controller

cache/Controller has the low level interface, used by the shared informers. It has a Run method
and HasSynced - which takes into account the Queue. It puts data in a Store, but a dummy 
can be provided - Add, Update, Delete, Replace will be called. The ListerWatcher abstracts the rest API.

It works with a Reflector - which watches the resource. 

The comments in the code provide insights on why the client is 'complex':
- if apiserver or etcd are upgraded, a large number of watchers will reconnect
- some query parameters can't be served from cache and hit etcd (the database) directly
- in absence of pagination - it is possible to serve from cache - and is similar to streaming.

*Issue*: the code does list() returning the entire data set. 

## ResourceVersion

Critical for sync - "Result is at least as fresh as the provided RV" - allow getting 
changes without going back to older entries. This is critical for reconnect.

Metadata.creationTimestap (second), namespace/name, uid and resourceVersion make K8S a 
sync optimized database. 

### Rest API

*TL;DR*:
- don't use pagination 
- backoff/retry
- use RV on reconnect
- use AllowWatchBookmarks in watch (cheaper)
- use AllowWatchBookmakrs in list if server supports it - streaming.

List() interface in Dynamic is pretty dangerous - returns ALL objects, which can be a very large object 
and spike in memory. Pagination - according to the comments in reflector - is expensive.

Best appears to be the 'listWatch' which is stream based instead of paging.
Uses fake "Added" events up to a "Bookmark" containing current RecentVersion and then real
events.

A custom list using the REST interface may also work well on client - incrementally parsing
the stream from the server. TODO: does the server batch or streams ? 

```go
       options := metav1.ListOptions{
			ResourceVersion:      lastKnownRV,
			AllowWatchBookmarks:  true,
			SendInitialEvents:    pointer.Bool(true),
			ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
			TimeoutSeconds:       &timeoutSeconds,
		}
		start := r.clock.Now()

```

An interesting comment in reflector.go that impacts native use: 

```go
	// WatchListPageSize is the requested chunk size of initial and resync watch lists.
	// If unset, for consistent reads (RV="") or reads that opt-into arbitrarily old data
	// (RV="0") it will default to pager.PageSize, for the rest (RV != "" && RV != "0")
	// it will turn off pagination to allow serving them from watch cache.
	// NOTE: It should be used carefully as paginated lists are always served directly from
	// etcd, which is significantly less efficient and may lead to serious performance and
	// scalability problems.
WatchListPageSize int64

  // UseWatchList if turned on instructs the reflector to open a stream to bring data from the API server.
  // Streaming has the primary advantage of using fewer server's resources to fetch data.
  //
  // The old behaviour establishes a LIST request which gets data in chunks.
  // Paginated list is less efficient and depending on the actual size of objects
  // might result in an increased memory consumption of the APIServer.
  //
  // See https://github.com/kubernetes/enhancements/tree/master/keps/sig-api-machinery/3157-watch-list#design-details
  UseWatchList bool

  // We used to make the call every 1sec (1 QPS), the goal here is to achieve ~98% traffic reduction when
  // API server is not healthy. With these parameters, backoff will stop at [30,60) sec interval which is
  // 0.22 QPS. If we don't backoff for 2min, assume API server is healthy and we reset the backoff.
  backoffManager:    wait.NewExponentialBackoffManager(800*time.Millisecond, 30*time.Second, 2*time.Minute, 2.0, 1.0, reflectorClock),

```




## REST usage

The List operation looks like: 

```shell
curl -v -XGET  -H "Accept: application/json" -H "Authorization: Bearer ..." 
 'https://35.193.24.39/api/v1/pods?allowWatchBookmarks=true&resourceVersion=0&resourceVersionMatch=NotOlderThan&timeoutSeconds=3'
```

The RESTClient object in K8S is intended for a single type - you need to pass a config
including the URL (GroupVersion --> /api/version/group in the URL). In could be used
with any REST server using the pattern /api/x/y and the rest of K8S params, including
proto and json support, and is coupled with Encoder/Decoder objects.
The encoders/decoders are based on ObjectCreator and json encoding, with Scheme used to
hold a registry (but possible to use an ObjectCreator directly too).

The RESTClient also deals with backoff/rate limiter (KUBE_CLIENT_BACKOFF_BASE/DURATION env), caches
the tokens, retries and has nice logs showing the curl equivalent command (transport/round_trippers.go.
For reference: klog level 6 shows timing, 7 URL/request headers, 8 response headers, 9 curl command too. Unfortunately
using klog.Printf(), not structured - can be sent to a logr.Logger, but only as msg. It can be used
as a general-purpose wrapper too, with any RoundTripper. It also include proxy support - but not for H2 (according
to docks), socks is supported. Auth with BearerToken, BearerTokenFile, User/pass, client cert - as well as oauth2

The client wraps moby/spdystream which is an early and incomplete implementation of H2 - but with no flow control, 
use only where required.

In particular with Watch, the REST client is used with generated interfaces - but they 
can also be manually created. For Get it is possible to use 'raw' data - and decode 
manually. Base interface is runtime.Object, and must have DeepCopy methods.

The generated interfaces are a bit bloated - they decode all the fileds, even if you
only need a couple. 

## Other useful K8S info

For auth/bootstraping, 'kubectl clusterinfo dump' has a lot of info.

Internally, it calls:
- "nodes", which has podCIDR for each node - no need for lookup, Addresses - internal, external and hostname.
- events in kube-system
- services - selectors, IPs
- pods/calico-xxx/logs?container=install-cni,...

# API servers

Using the sample: 
``` --secure-port 8443 --etcd-servers=dummy  ```
--cert-dir defaults to apiserver.local.config/certificates
--feature-gates WatchList=true

'GenericAPIServer'

# Generating code using k8s codegen

Few observations (after spending far too much time debugging client-gen):

- current (1.28) generators expect a GOPATH/src/github.com/NAME/ hierarchy. The trick is to create a temp dir and symlink.
- crd generator is happy without this trick. It's the most important of the tools.
- uk8s makefile has a working example, with the different styles of clients broken apart.

# HTTP URL structure - kubectl view

/api and /apis are used by kubectl for discovery with "Accept: application/json;g=apidiscovery.k8s.io;v=v2beta1;as=APIGroupDiscoveryList,application/json",
and the result cached in ~/.kube/cache/discovery/SERVER/GROUP/VERSION/serverresources.json

The /api/ is used for core componets - Pod, etc.

This is handled by the API server, using the CRD info. Creating a standalone version to embed in a regular http server is possible
but likely wasteful - just use a k8s APIserver. Easiest would be to just copy the json from the cache and link it in.

CRDs are /apis/GROUP/VERSION/namespaces/NAMESPACE/RESOURCE with "Accept: application/json;as=Table;v=v1;g=meta.k8s.io,application/json;as=Table;v=v1beta1;g=meta.k8s.io,application/json"

The 'table' response type is slightly different from the yaml - includes the columns defined in the CRD.

The '-o yaml' results in  "Accept: application/json" and ?limit=500 requests.

Watch (-w) is a list followed by ?resourceVersion=1109399448&watch=true' 

