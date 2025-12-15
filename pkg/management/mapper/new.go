package mapper

import (
	"github.com/openshift/monitoring-plugin/pkg/k8s"
	"github.com/prometheus/prometheus/model/relabel"
)

// New creates a new instance of the mapper client.
func New(k8sClient k8s.Client) Client {
	return &mapper{
		k8sClient:           k8sClient,
		prometheusRules:     make(map[PrometheusRuleId][]PrometheusAlertRuleId),
		alertRelabelConfigs: make([]*relabel.Config, 0),
	}
}
