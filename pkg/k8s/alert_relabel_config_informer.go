package k8s

import (
	"context"

	osmv1 "github.com/openshift/api/monitoring/v1"
	osmv1client "github.com/openshift/client-go/monitoring/clientset/versioned"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"
)

type alertRelabelConfigInformer struct {
	informer cache.SharedIndexInformer
}

func newAlertRelabelConfigInformer(ctx context.Context, clientset *osmv1client.Clientset) (*alertRelabelConfigInformer, error) {
	informer := cache.NewSharedIndexInformer(
		alertRelabelConfigListWatchForAllNamespaces(clientset),
		&osmv1.AlertRelabelConfig{},
		0,
		cache.Indexers{},
	)

	arci := &alertRelabelConfigInformer{
		informer: informer,
	}

	go arci.informer.Run(ctx.Done())

	cache.WaitForNamedCacheSync("AlertRelabelConfig informer", ctx.Done(),
		arci.informer.HasSynced,
	)

	return arci, nil
}

func alertRelabelConfigListWatchForAllNamespaces(clientset *osmv1client.Clientset) *cache.ListWatch {
	return cache.NewListWatchFromClient(clientset.MonitoringV1().RESTClient(), "alertrelabelconfigs", "", fields.Everything())
}

func (arci *alertRelabelConfigInformer) AddCallbacks(callbacks AlertRelabelConfigInformerCallback) error {
	_, err := arci.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			arc, ok := obj.(*osmv1.AlertRelabelConfig)
			if !ok {
				return
			}
			callbacks.OnAdd(arc)
		},
		UpdateFunc: func(oldObj interface{}, newObj interface{}) {
			arc, ok := newObj.(*osmv1.AlertRelabelConfig)
			if !ok {
				return
			}
			callbacks.OnUpdate(arc)
		},
		DeleteFunc: func(obj interface{}) {
			k, err := cache.DeletionHandlingObjectToName(obj)
			if err != nil {
				return
			}
			callbacks.OnDelete(k)
		},
	})

	return err
}
