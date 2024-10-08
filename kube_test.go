package mk8s

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/flowcontrol"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

// k8s.io/client-go/kubernetes/scheme is the generated package - by client-gen
// starts with runtime.NewScheme(),

// TODO: some testing with Informer and check the Store.
// TODO: can we use Informer with disk cache and saved lastSync ?

func ExampleStart()  {
	ctx := context.Background()
	ks, err := New(ctx, nil)
	if err != nil {
		return
	}
	fmt.Println(ks.Default != nil)
	// Output: true
}


func TestModes(t *testing.T) {
	SetK8SLogging("-v=9")
	ctx, cf := context.WithTimeout(context.Background(), 5*time.Second)
	defer cf()

	ks, err := New(ctx, nil)

	k := ks.Default

	rc, err := k.RestClient("", "v1")
	if err != nil {
		t.Fatal(err)
	}

	// Use the RestClient to create
	t.Run("rest-create", func(t *testing.T) {
		// Cleanup first
		err = k.Client().CoreV1().ConfigMaps("default").Delete(ctx, "test-create",
			metav1.DeleteOptions{})

		obj := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-create",
				Namespace: "default"},
			Data: map[string]string{"a": "b"}}

		// Create a request - only works the first time (otherwise 'conflict'
		kr := rc.Post().Body(obj).Resource("configmaps").
			Namespace("default")
		kres := kr.Do(ctx)
		if kres.Error() != nil {
			t.Fatal(kres.Error())
		}

		// Raw returns the json bytes, raw format.
		resb, _ := kres.Raw()
		log.Println(len(resb))

		pl, _ := kres.Get()
		log.Println(pl)

		// Verify it was set
		cm, err := k.Client().CoreV1().ConfigMaps("default").Get(ctx,
			"test-create", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if cm.Data["a"] != "b" {
			t.Error("not found", cm)
		}
	})

	// Use the RestClient to create
	t.Run("rest-update", func(t *testing.T) {
		obj := &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "update", Namespace: "default"}}
		cm, err := k.Client().CoreV1().ConfigMaps("default").Get(ctx, "update", metav1.GetOptions{})
		if err != nil {
			cm, err = k.Client().CoreV1().ConfigMaps("default").Create(ctx, obj, metav1.CreateOptions{})
		}

		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data["a"] = "d"

		// Create a request - only works the first time (otherwise 'conflict'
		kres := rc.Put().Body(cm).Resource("configmaps").
			Namespace("default").Name("update").Do(ctx)

		cm, err = k.Client().CoreV1().ConfigMaps("default").Get(ctx, "update", metav1.GetOptions{})
		log.Println(cm)

		if kres.Error() != nil {
			t.Fatal(kres.Error())
		}
		resb, err := kres.Raw()
		log.Println(len(resb))

		pl, err := kres.Get()
		log.Println(pl)

		t.Run("rest-patch", func(t *testing.T) {
			// Create a request - only works the first time (otherwise 'conflict'
			kres := rc.Patch(types.JSONPatchType).
				Body([]byte(`[{"op":"replace", "value":"c", "path":"/data/a"}]`)).
				Resource("configmaps").
				Namespace("default").
				Name("update").Do(ctx)

			// Other options: merge, apply, strategicmerge

			resb, err := kres.Raw()
			if err != nil {
				t.Fatal(err)
			}
			log.Println(len(resb))

			pl, err := kres.Get()
			log.Println(pl)
			cm, err := k.Client().CoreV1().ConfigMaps("default").Get(ctx,
				"update", metav1.GetOptions{})
			if cm.Data["a"] != "c" {
				t.Error("not found", cm)
			}
		})
	})

	t.Run("raw", func(t *testing.T) {
		obj := &v1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "fooraw", Namespace: "default"}}
		body, _ := runtime.Encode(scheme.Codecs.LegacyCodec(v1.SchemeGroupVersion), obj)

		url, _, _ := rest.DefaultServerUrlFor(k.RestConfig)
		hc, _ := rest.HTTPClientFor(k.RestConfig)

		r, err := http.NewRequestWithContext(ctx, "POST",
			url.String()+"/api/v1/namespaces/default/configmaps", bytes.NewReader(body))
		res, err := hc.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resb, err := ioutil.ReadAll(res.Body)
		log.Println(string(resb))

	})

}

// Normally implemented by client.RESTClient - this is the interface that can be used when creating clientset.New() clients.
// Incorporates base and options and a configured HttpClient.
//
// rest.HTTPClientFor(config) is configuring the http client for NewForConfig, with timeout, transport - NewForConfigAndClient is using a custom one.
type FakeRest struct {}

func (f FakeRest) GetRateLimiter() flowcontrol.RateLimiter {
	//TODO implement me
	panic("implement me")
}

func (f FakeRest) Verb(verb string) *rest.Request {
	//TODO implement me
	panic("implement me")
}

func (f FakeRest) Post() *rest.Request {
	//TODO implement me
	panic("implement me")
}

func (f FakeRest) Put() *rest.Request {
	//TODO implement me
	panic("implement me")
}

func (f FakeRest) Patch(pt types.PatchType) *rest.Request {
	//TODO implement me
	panic("implement me")
}

func (f FakeRest) Get() *rest.Request {
	//TODO implement me
	panic("implement me")
}

func (f FakeRest) Delete() *rest.Request {
	//TODO implement me
	panic("implement me")
}

func (f FakeRest) APIVersion() schema.GroupVersion {
	//TODO implement me
	panic("implement me")
}

var _ rest.Interface = &FakeRest{}

// Direct watch using the client.
func TestWatch(t *testing.T) {
	SetK8SLogging("-v=9")

	ks, err := New(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	k := ks.Default

	t.Run("raw", func(t *testing.T) {
		ctx, cf := context.WithTimeout(context.Background(), 50*time.Second)
		defer cf()

		url, _, _ := rest.DefaultServerUrlFor(k.RestConfig)
		hc, _ := rest.HTTPClientFor(k.RestConfig)

		r, err := http.NewRequestWithContext(ctx, "GET",
			url.String()+"/api/v1/pods?fieldSelector=metadata.namespace%3D%3Distio-system%2Cstatus.phase%21%3DPending&labelSelector=tier%21%3Dprod%2C+a%21%3Db&limit=3&timeoutSeconds=3", nil)
		res, err := hc.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resb, err := ioutil.ReadAll(res.Body)
		log.Println(string(resb))

	})

	t.Run("rawwatch", func(t *testing.T) {
		ctx, cf := context.WithTimeout(context.Background(), 3*time.Second)
		defer cf()

		url, _, _ := rest.DefaultServerUrlFor(k.RestConfig)
		hc, _ := rest.HTTPClientFor(k.RestConfig)

		r, err := http.NewRequestWithContext(ctx, "GET", url.String()+"/api/v1/events?watch=1", nil)
		res, err := hc.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		frameReader := json.Framer.NewFrameReader(res.Body)
		buf := make([]byte, 16*1024)
		for {
			n, err := frameReader.Read(buf)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				t.Fatal(err)
			}
			log.Println(string(buf[0:n]))
		}

	})

	// Use the RestClient
	t.Run("rest", func(t *testing.T) {
		ctx, cf := context.WithTimeout(context.Background(), 5*time.Second)
		defer cf()

		// k8s.io/client-go/kubernetes/scheme is the generated package - by client-gen
		// starts with runtime.NewScheme(),

		rc, err := k.RestClient("", "v1")
		if err != nil {
			t.Fatal(err)
		}
		kr := rc.Get()
		// namepspace, name
		kr.Resource("pods")

		// Request changes to /api/v1/namespaces/istio-system/pods?FieldSelector=...
		// Without: /api/v1/pods
		//kr.Namespace("istio-system")
		kr.Param("limit", "3")
		// metadata.namespace==istio-system
		kr.Param("FieldSelector", "status.phase!=Pending")

		kres := kr.Do(ctx)

		// Typical headers:
		// Audit-Id: ...

		// List response has the resourceVersion, continue
		// and remainingItemCount

		//r, err := http.NewRequestWithContext(ctx, "GET", "https://35.193.24.39/api/v1/pods?fieldSelector=metadata.namespace%3D%3Distio-system%2Cstatus.phase%21%3DPending&labelSelector=tier%21%3Dprod%2C+a%21%3Db&limit=3&timeoutSeconds=3", nil)
		//res, err := k.httpClient.Do(r)
		//resb, err := kres.Raw() // Raw json response - no transformation
		//if err != nil {
		//	t.Fatal(err)
		//}
		////resb, err := ioutil.ReadAll(res.Body)
		//log.Println(len(resb))

		// Works if the generated config is registered. The 'raw' just return json
		// that can be unmarshalled on any struct.
		pl, err := kres.Get()
		// PodList object, with ListMeta
		log.Println(pl)
	})

	t.Run("rest-istio", func(t *testing.T) {
		ctx, cf := context.WithTimeout(context.Background(), 5*time.Second)
		defer cf()

		// k8s.io/client-go/kubernetes/scheme is the generated package - by client-gen
		// starts with runtime.NewScheme(),

		rc, err := k.RestClient("networking.istio.io", "v1")
		if err != nil {
			t.Fatal(err)
		}
		kr := rc.Get()
		// namepspace, name
		kr.Resource("serviceentries")

		// Request changes to /apis/networking.istio.io/v1/namespaces/istio-system/serviceentries?FieldSelector=...
		// Without: /apis/networking.istio.io/v1/serviceentries
		//kr.Namespace("ambient-l4")
		kr.Param("limit", "3")
		// metadata.namespace==istio-system
		//kr.Param("FieldSelector", "status.phase!=Pending")

		kres := kr.Do(ctx)
		resb, err := kres.Raw() // Raw json response - no transformation
		if err != nil {
			t.Fatal(err)
		}

		log.Println(len(resb))
	})

	//t.Run("restcached", func(t *testing.T) {
	//	ctx, cf := context.WithTimeout(context.Background(), 5*time.Second)
	//	defer cf()
	//
	//	hd := homedir.HomeDir()
	//	cache := path.Join(hd, ".kube", "cache")
	//
	//	// Using ~/.kube/cache/discovery, holds a groupToServerResources map.
	//
	//	cachedClient, err := disk.NewCachedDiscoveryClientForConfig(k.ConfigFor("/api", "v1", "", scheme.Codecs.WithoutConversion()),
	//		path.Join(cache, "discovery"),
	//		path.Join(cache, "http"), 1*time.Hour)
	//
	//	//cachedClient.Invalidate()
	//
	//	// original client
	//	rc := cachedClient.RESTClient() // rest.Interface
	//
	//	//rl, err := cachedClient.ServerResourcesForGroupVersion("pods/v1")
	//	//log.Println(rl, err)
	//
	//	//r, err := http.NewRequestWithContext(ctx, "GET", url.String()+"/api/v1/events?watch=1", nil)
	//
	//	kr := rc.Get()
	//	kr.RequestURI("/api/v1/pods")
	//	kres := kr.Do(ctx)
	//	resb, err := kres.Raw()
	//
	//	kr = rc.Get()
	//	kr.RequestURI("/api/v1/pods")
	//	kres = kr.Do(ctx)
	//	resb, err = kres.Raw()
	//
	//	if err != nil {
	//		t.Fatal(err)
	//	}
	//
	//	//resb, err := ioutil.ReadAll(res.Body)
	//	log.Println(len(resb))
	//
	//	pl, err := kres.Get()
	//	// PodList object, with ListMeta
	//	log.Println(pl)
	//})

	t.Run("restwatch", func(t *testing.T) {
		ctx, cf := context.WithTimeout(context.Background(), 5*time.Second)
		defer cf()

		// k8s.io/client-go/kubernetes/scheme is the generated package - by client-gen
		// starts with runtime.NewScheme(),

		rc, err := k.RestClient( "", "v1")
		if err != nil {
			t.Fatal(err)
		}

		kr := rc.Get()
		kr.Resource("events")
		kr.Param("limit", "3")
		res := kr.Do(ctx)
		log.Println(res.Get())

		kr = rc.Get()

		kr.Resource("events")
		kr.Param("limit", "3")
		kr.Param("watch", "1")

		wi, err := kr.Watch(ctx)

		if err != nil {
			t.Fatal(err)
		}
		for k := range wi.ResultChan() {
			log.Println(k)
			break
		}
	})

	d, err := dynamic.NewForConfig(k.RestConfig)

	t.Run("dyn", func(t *testing.T) {
		pods := d.Resource(schema.GroupVersionResource{
			Version:  "v1",
			Group:    "",
			Resource: "pods",
		})
		ctx := context.Background()

		ur, err := pods.List(ctx, metav1.ListOptions{
			//Watch:                true,
			//SendInitialEvents:    Bool(true),
			//AllowWatchBookmarks: true,
			//ResourceVersion:     "0",
			//ResourceVersionMatch: "NotOlderThan",
			Limit:          3,
			FieldSelector:  "metadata.namespace==istio-system,status.phase!=Pending",
			LabelSelector:  "tier!=prod, a!=b",
			TimeoutSeconds: PInt64(3),
		})
		if err != nil {
			t.Fatal(err)
		}
		// listResult includes:
		// Object - kind, apiVersion, metadata(resourceVersion="")
		// Items - Unstructured objects, containing:
		//   - Object (kind, status, metadata, apiVersion, spec)
		// maps of string[] interface derived from parsing the raw JSON

		for _, p := range ur.Items {
			pod := &v1.Pod{}

			runtime.DefaultUnstructuredConverter.FromUnstructured(p.Object, pod)
			ns, _, _ := unstructured.NestedString(p.Object, "metadata", "namespace")

			log.Println(ns, p.GetName(), p.GetResourceVersion(), pod.Spec.Containers[0].Image, pod.Labels)
		}

		log.Println("cont", ur.GetContinue(), "ver", ur.GetResourceVersion(),
			"remainingItemCount", ur.GetRemainingItemCount())

	})
}

// According to https://kubernetes.io/docs/reference/using-api/api-concepts/#watch-bookmarks
// streaming lists is in 1.27 alpha.
func TestSendInitialEvents(t *testing.T) {
	SetK8SLogging("-v=9")
	k, err := New(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}

	//GET /api/v1/namespaces/test/pods?watch=1&sendInitialEvents=true&allowWatchBookmarks=true&resourceVersion=&resourceVersionMatch=NotOlderThan
	//var timeout time.Duration
	//if opts.TimeoutSeconds != nil {
	//timeout = time.Duration(5) * time.Second
	//}

	// Forbidden - sendInitialEvents forbidden unless WatchList feature
	// gate is enabled - in GKE in 1.27 as alpha

	opts := metav1.ListOptions{
		Watch:                true,
		SendInitialEvents:    Bool(true),
		AllowWatchBookmarks:  true,
		ResourceVersion:      "0",
		ResourceVersionMatch: "NotOlderThan",
		TimeoutSeconds:       PInt64(3),
	}
	d, err := dynamic.NewForConfig(k.Default.RestConfig)
	opts.Watch = true
	pch, err := d.Resource(schema.GroupVersionResource{
		Version:  "v1",
		Group:    "",
		Resource: "pods",
	}).Namespace("").
		Watch(context.Background(), opts)

	//VersionedParams(&opts, scheme.ParameterCodec).

	//kc1 = kc1.Param("watch", "true").
	//	Param("allowWatchBookmarks", "true").
	//	Param("sendInitialEvents", "true").
	//	Param("allowWatchBookmarks", "true")

	//kc1 = kc1.SpecificallyVersionedParams()
	//	kc1.Prefix("api/v1")
	//pch, err := kc1.
	//	Timeout(timeout).
	//	Watch(context.Background())
	if err != nil {
		t.Skip("sendInitialEventsFeatureGate not enabled")
	}

	// https://35.193.24.39/api/v1/pod?allowWatchBookmarks=true&sendInitialEvents=true&timeoutSeconds=5&watch=true
	// https://35.193.24.39/api/v1/pods?allowWatchBookmarks=true&sendInitialEvents=true&timeout=5s&timeoutSeconds=5&watch=true
	//pch, err = k.Client.CoreV1().Pods("").Watch(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}

	for e := range pch.ResultChan() {
		//p := e.Object.(*v1.Pod)
		if e.Type == "ADDED" {
			p := e.Object.(*unstructured.Unstructured)
			pod := &v1.Pod{}
			runtime.DefaultUnstructuredConverter.FromUnstructured(p.Object,
				pod)
			ns, _, _ := unstructured.NestedString(p.Object, "metadata", "namespace")

			log.Println(ns, p.GetName(), p.GetResourceVersion(), pod.Spec.Containers[0].Image)
		} else if e.Type == "BOOKMARK" {
			log.Println(e.Type, e.Object)
		} else {
			p := e.Object.(*unstructured.Unstructured)
			log.Println(e.Type, p.GetNamespace(), p.GetName(), p.GetResourceVersion())
		}
	}

	SetK8SLogging("-v=0")
	pch, err = d.Resource(schema.GroupVersionResource{
		Version:  "v1",
		Group:    "",
		Resource: "pods",
	}).Namespace("").
		Watch(context.Background(), opts)

}

func Bool(b bool) *bool {
	return &b
}

func PInt64(b int64) *int64 {
	return &b
}
