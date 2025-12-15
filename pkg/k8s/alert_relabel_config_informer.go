package k8s

import (
	"context"
	"fmt"

	osmv1 "github.com/openshift/api/monitoring/v1"
	osmv1client "github.com/openshift/client-go/monitoring/clientset/versioned"
	"github.com/prometheus/prometheus/model/relabel"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert/yaml"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"
)

const (
	AlertRelabelConfigSecretName = "alert-relabel-configs"
	AlertRelabelConfigSecretKey  = "config.yaml"
)

var log = logrus.WithField("module", "k8s")

type alertRelabelConfigInformer struct {
	arcInformer    cache.SharedIndexInformer
	secretInformer cache.SharedIndexInformer
	configs        []*relabel.Config
}

func newAlertRelabelConfigInformer(ctx context.Context, clientset *osmv1client.Clientset) (*alertRelabelConfigInformer, error) {
	informer := cache.NewSharedIndexInformer(
		alertRelabelConfigListWatchForAllNamespaces(clientset),
		&osmv1.AlertRelabelConfig{},
		0,
		cache.Indexers{},
	)

	secretInformer := cache.NewSharedIndexInformer(
		alertRelabelConfigSecretListWatch(clientset, ""),
		&corev1.Secret{},
		0,
		cache.Indexers{},
	)

	arci := &alertRelabelConfigInformer{
		arcInformer:    informer,
		secretInformer: secretInformer,
	}

	_, err := arci.secretInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			arci.updateConfigs(obj)
		},
		UpdateFunc: func(oldObj interface{}, newObj interface{}) {
			arci.updateConfigs(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			arci.configs = []*relabel.Config{}
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add event handler to secret informer: %w", err)
	}

	go arci.arcInformer.Run(ctx.Done())
	go arci.secretInformer.Run(ctx.Done())

	cache.WaitForNamedCacheSync("AlertRelabelConfig informer", ctx.Done(),
		arci.arcInformer.HasSynced,
		arci.secretInformer.HasSynced,
	)

	return arci, nil
}

func alertRelabelConfigListWatchForAllNamespaces(clientset *osmv1client.Clientset) *cache.ListWatch {
	return cache.NewListWatchFromClient(clientset.MonitoringV1().RESTClient(), "alertrelabelconfigs", "", fields.Everything())
}

func alertRelabelConfigSecretListWatch(clientset *osmv1client.Clientset, namespace string) *cache.ListWatch {
	return cache.NewListWatchFromClient(
		clientset.Discovery().RESTClient(),
		"secrets",
		namespace,
		fields.OneTermEqualSelector("metadata.name", AlertRelabelConfigSecretName),
	)
}

func (arci *alertRelabelConfigInformer) updateConfigs(obj interface{}) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		log.Errorf("unexpected object type in secret store: %T", obj)
	}

	configData, ok := secret.Data[AlertRelabelConfigSecretKey]
	if !ok {
		log.Errorf("no config data found in secret %q", secret.Name)
	}

	var configs []*relabel.Config
	if err := yaml.Unmarshal(configData, &configs); err != nil {
		log.Errorf("failed to unmarshal relabel configs: %v", err)
	}

	arci.configs = configs
}
