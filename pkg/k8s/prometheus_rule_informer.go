package k8s

import (
	"context"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringv1client "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"
)

type prometheusRuleInformer struct {
	informer cache.SharedIndexInformer
}

func newPrometheusRuleInformer(ctx context.Context, clientset *monitoringv1client.Clientset) (*prometheusRuleInformer, error) {
	informer := cache.NewSharedIndexInformer(
		prometheusRuleListWatchForAllNamespaces(clientset),
		&monitoringv1.PrometheusRule{},
		0,
		cache.Indexers{},
	)

	pri := &prometheusRuleInformer{
		informer: informer,
	}

	go pri.informer.Run(ctx.Done())

	cache.WaitForNamedCacheSync("PrometheusRule informer", ctx.Done(),
		pri.informer.HasSynced,
	)

	return pri, nil
}

func prometheusRuleListWatchForAllNamespaces(clientset *monitoringv1client.Clientset) *cache.ListWatch {
	return cache.NewListWatchFromClient(clientset.MonitoringV1().RESTClient(), "prometheusrules", "", fields.Everything())
}

func (pri *prometheusRuleInformer) AddCallbacks(callbacks PrometheusRuleInformerCallback) error {
	_, err := pri.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pr, ok := obj.(*monitoringv1.PrometheusRule)
			if !ok {
				return
			}
			callbacks.OnAdd(pr)
		},
		UpdateFunc: func(oldObj interface{}, newObj interface{}) {
			pr, ok := newObj.(*monitoringv1.PrometheusRule)
			if !ok {
				return
			}
			callbacks.OnUpdate(pr)
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
