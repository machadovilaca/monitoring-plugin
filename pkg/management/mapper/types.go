package mapper

import (
	"context"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
)

// PrometheusRuleId is a unique identifier for a PrometheusRule resource in Kubernetes, represented by its NamespacedName.
type PrometheusRuleId types.NamespacedName

// AlertRelabelConfigId is a unique identifier for an AlertRelabelConfig resource in Kubernetes, represented by its NamespacedName.
type AlertRelabelConfigId types.NamespacedName

// PrometheusAlertRuleId is a hash-based identifier for an alerting rule within a PrometheusRule, represented by a string.
type PrometheusAlertRuleId string

// Client defines the interface for mapping between Prometheus alerting rules and their unique identifiers.
type Client interface {
	// GetAlertingRuleId returns the unique identifier for a given alerting rule.
	GetAlertingRuleId(alertRule *monitoringv1.Rule) PrometheusAlertRuleId

	// FindAlertRuleById returns the PrometheusRuleId for a given alerting rule ID.
	FindAlertRuleById(alertRuleId PrometheusAlertRuleId) (*PrometheusRuleId, error)

	// WatchPrometheusRules starts watching for changes to PrometheusRules.
	WatchPrometheusRules(ctx context.Context)

	// AddPrometheusRule adds or updates a PrometheusRule in the mapper.
	AddPrometheusRule(pr *monitoringv1.PrometheusRule)

	// DeletePrometheusRule removes a PrometheusRule from the mapper.
	DeletePrometheusRule(key cache.ObjectName)
}
