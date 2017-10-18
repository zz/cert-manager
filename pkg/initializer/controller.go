package initializer

import (
	"context"
	"fmt"
	"github.com/golang/glog"
	"github.com/jetstack-experimental/cert-manager/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

func (g *GenericInitializer) worker(stopCh <-chan struct{}) {
	glog.V(4).Infof("Starting worker")
	for {
		obj, shutdown := g.queue.Get()
		if shutdown {
			break
		}

		var item queueItem
		err := func(obj interface{}) error {
			defer g.queue.Done(obj)
			var ok bool
			if item, ok = obj.(queueItem); !ok {
				return nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = util.ContextWithStopCh(ctx, stopCh)
			glog.Infof("Syncing item '%s'", item.key)
			if err := g.processNextWorkItem(ctx, item); err != nil {
				return err
			}
			g.queue.Forget(obj)
			return nil
		}(obj)

		if err != nil {
			glog.Errorf("Re-queuing item %q due to error processing: %s", item.key, err.Error())
			g.queue.AddRateLimited(obj)
			continue
		}

		glog.Infof("Finished processing work item %q", item.key)
	}
	glog.V(4).Infof("Exiting worker loop")
}

func (g *GenericInitializer) processNextWorkItem(ctx context.Context, item queueItem) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(item.key)
	if err != nil {
		glog.Errorf("Invalid resource key: %s", item.key)
		return nil
	}

	lister, err := g.listerForGvk(item.gvk)
	if err != nil {
		return fmt.Errorf("error getting lister: %v", err)
	}

	listerGetFn := lister.Get
	if namespace != "" {
		listerGetFn = lister.ByNamespace(namespace).Get
	}

	runtimeObj, err := listerGetFn(name)
	if err != nil {
		glog.Errorf("Failed to get object: %s", err.Error())
		return err
	}

	var metav1Obj metav1.Object
	var ok bool
	if metav1Obj, ok = runtimeObj.(metav1.Object); !ok {
		s := fmt.Sprintf("Object does not implement metav1.Object")
		glog.Errorf(s)
		return fmt.Errorf(s)
	}

	initializers := metav1Obj.GetInitializers()
	if initializers == nil || len(initializers.Pending) == 0 || initializers.Pending[0].Name != g.name {
		glog.V(4).Infof("Initializer %q found on object %q", g.name, name)
		return nil
	}

	// Run defaulting functions
	runtimeObjCopy := runtimeObj.DeepCopyObject()
	g.Scheme.Default(runtimeObjCopy)

	if metav1Obj, ok = runtimeObjCopy.(metav1.Object); !ok {
		s := fmt.Sprintf("Object copy does not implement metav1.Object")
		glog.Errorf(s)
		return fmt.Errorf(s)
	}
	initializers = metav1Obj.GetInitializers()
	initializers.Pending = initializers.Pending[1:]
	metav1Obj.SetInitializers(initializers)

	mapping, err := g.mapper.RESTMapping(schema.GroupKind{Group: item.gvk.Group, Kind: item.gvk.Kind}, item.gvk.Version)
	if err != nil {
		return fmt.Errorf("cannot get resource mapping for %q: %v", item.gvk, err)
	}

	req := g.client.Post().Resource(mapping.Resource).Body(metav1Obj)
	if namespace != "" {
		req = req.Namespace(namespace)
	}

	_, err = req.Do().Raw()
	if err != nil {
		s := fmt.Sprintf("Error saving resource %q: %v", name, err)
		glog.Errorf(s)
		return err
	}

	return nil
}

func (g *GenericInitializer) listerForGvk(gvk schema.GroupVersionKind) (cache.GenericLister, error) {
	var informer cache.SharedIndexInformer
	var ok bool
	if informer, ok = g.informers[gvk]; !ok {
		return nil, fmt.Errorf("informer for %q not registered", gvk)
	}
	mapping, err := g.mapper.RESTMapping(schema.GroupKind{Group: gvk.Group, Kind: gvk.Kind}, gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("cannot get resource mapping for %q: %v", gvk, err)
	}
	return cache.NewGenericLister(informer.GetIndexer(), schema.GroupResource{Group: gvk.Group, Resource: mapping.Resource}), nil
}
