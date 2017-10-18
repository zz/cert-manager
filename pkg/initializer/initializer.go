package initializer

import (
	"fmt"
	"time"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	cacheddiscovery "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type GenericInitializer struct {
	Scheme *runtime.Scheme

	name string

	client    rest.Interface
	mapper    *discovery.DeferredDiscoveryRESTMapper
	informers map[schema.GroupVersionKind]cache.SharedIndexInformer
	listers   map[schema.GroupVersionKind]cache.GenericLister

	queue workqueue.RateLimitingInterface
}

func NewGenericInitializer(restClient rest.Interface, discoveryInterface discovery.DiscoveryInterface, versionInterfacesFunc meta.VersionInterfacesFunc, scheme *runtime.Scheme, informers map[schema.GroupVersionKind]cache.SharedIndexInformer) *GenericInitializer {
	cachedDiscovery := cacheddiscovery.NewMemCacheClient(discoveryInterface)
	mapper := discovery.NewDeferredDiscoveryRESTMapper(cachedDiscovery, versionInterfacesFunc)
	g := &GenericInitializer{
		Scheme:    scheme,
		name:      "TODO-put-initializer-name",
		client:    restClient,
		mapper:    mapper,
		informers: informers,
		queue:     workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "initializers"),
	}
	// TODO: periodically invalidate cache
	cachedDiscovery.Invalidate()
	for _, informer := range informers {
		informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: g.addFunc,
			UpdateFunc: func(old, cur interface{}) {
				oldObj := old.(metav1.Object)
				curObj := cur.(metav1.Object)
				if oldObj.GetResourceVersion() != curObj.GetResourceVersion() {
					g.addFunc(cur)
				}
			},
		})
	}

	return g
}

func (g *GenericInitializer) Run(workers int, stopCh <-chan struct{}) error {
	glog.V(4).Infof("Starting initializer control loop")
	hasSynced := make([]cache.InformerSynced, len(g.informers))
	i := 0
	for _, informer := range g.informers {
		hasSynced[i] = informer.HasSynced
		i++
	}
	// wait for all the informer caches we depend to sync
	if !cache.WaitForCacheSync(stopCh, hasSynced...) {
		return fmt.Errorf("error waiting for informer caches to sync")
	}

	glog.V(4).Infof("Synced all caches for initializer control loop")

	for i := 0; i < workers; i++ {
		// TODO (@munnerz): make time.Second duration configurable
		go wait.Until(func() { g.worker(stopCh) }, time.Second, stopCh)
	}
	<-stopCh
	glog.V(4).Infof("Shutting down queue as workqueue signaled shutdown")
	g.queue.ShutDown()
	return nil
}

func (g *GenericInitializer) enqueue(gvk schema.GroupVersionKind, obj interface{}) {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		glog.Errorf("Error getting key for object: %v", err)
		return
	}
	g.queue.AddRateLimited(queueItem{gvk: gvk, key: key})
}

func (g *GenericInitializer) addFunc(obj interface{}) {
	var runtimeObj runtime.Object
	var ok bool
	if runtimeObj, ok = obj.(runtime.Object); !ok {
		glog.Errorf("Object passed to addFunc does not implement runtime.Object")
		return
	}

	gvk := runtimeObj.GetObjectKind().GroupVersionKind()
	g.enqueue(gvk, obj)
}

type queueItem struct {
	gvk schema.GroupVersionKind
	key string
}
