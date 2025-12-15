package k8s

import (
	"context"
	"fmt"

	osmv1 "github.com/openshift/api/monitoring/v1"
	osmv1client "github.com/openshift/client-go/monitoring/clientset/versioned"
	"github.com/prometheus/prometheus/model/relabel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type alertRelabelConfigManager struct {
	clientset                  *osmv1client.Clientset
	alertRelabelConfigInformer *alertRelabelConfigInformer
}

func newAlertRelabelConfigManager(clientset *osmv1client.Clientset, informer *alertRelabelConfigInformer) *alertRelabelConfigManager {
	return &alertRelabelConfigManager{
		clientset:                  clientset,
		alertRelabelConfigInformer: informer,
	}
}

func (arcm *alertRelabelConfigManager) List(ctx context.Context, namespace string) ([]osmv1.AlertRelabelConfig, error) {
	arcs := arcm.alertRelabelConfigInformer.arcInformer.GetStore().List()

	alertRelabelConfigs := make([]osmv1.AlertRelabelConfig, 0, len(arcs))
	for _, item := range arcs {
		arc, ok := item.(*osmv1.AlertRelabelConfig)
		if !ok {
			continue
		}
		alertRelabelConfigs = append(alertRelabelConfigs, *arc)
	}

	return alertRelabelConfigs, nil
}

func (arcm *alertRelabelConfigManager) Get(ctx context.Context, namespace string, name string) (*osmv1.AlertRelabelConfig, bool, error) {
	item, exists, err := arcm.alertRelabelConfigInformer.arcInformer.GetStore().GetByKey(namespace + "/" + name)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}

	arc, ok := item.(*osmv1.AlertRelabelConfig)
	if !ok {
		return nil, false, nil
	}

	return arc, true, nil
}

func (arcm *alertRelabelConfigManager) Create(ctx context.Context, arc osmv1.AlertRelabelConfig) (*osmv1.AlertRelabelConfig, error) {
	created, err := arcm.clientset.MonitoringV1().AlertRelabelConfigs(arc.Namespace).Create(ctx, &arc, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create AlertRelabelConfig %s/%s: %w", arc.Namespace, arc.Name, err)
	}

	return created, nil
}

func (arcm *alertRelabelConfigManager) Update(ctx context.Context, arc osmv1.AlertRelabelConfig) error {
	_, err := arcm.clientset.MonitoringV1().AlertRelabelConfigs(arc.Namespace).Update(ctx, &arc, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update AlertRelabelConfig %s/%s: %w", arc.Namespace, arc.Name, err)
	}

	return nil
}

func (arcm *alertRelabelConfigManager) Delete(ctx context.Context, namespace string, name string) error {
	err := arcm.clientset.MonitoringV1().AlertRelabelConfigs(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete AlertRelabelConfig %s: %w", name, err)
	}

	return nil
}

func (arcm *alertRelabelConfigManager) GetRelabelConfigs(ctx context.Context) ([]*relabel.Config, error) {
	return arcm.alertRelabelConfigInformer.configs, nil
}
