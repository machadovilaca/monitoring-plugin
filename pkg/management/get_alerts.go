package management

import (
	"context"
	"fmt"

	"github.com/openshift/monitoring-plugin/pkg/alertcomponent"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"

	"github.com/openshift/monitoring-plugin/pkg/k8s"
)

func (c *client) GetAlerts(ctx context.Context, req k8s.GetAlertsRequest) ([]k8s.PrometheusAlert, error) {
	alerts, err := c.k8sClient.PrometheusAlerts().GetAlerts(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get prometheus alerts: %w", err)
	}

	configs := c.k8sClient.RelabeledRules().Config()
	rules := c.k8sClient.RelabeledRules().List(ctx)

	var result []k8s.PrometheusAlert
	for _, alert := range alerts {

		relabels, keep := relabel.Process(labels.FromMap(alert.Labels), configs...)
		if !keep {
			continue
		}

		alert.Labels = relabels.Map()

		// correlate alert -> base alert rule via subset matching against relabeled rules
		alertRuleId, component, layer := correlateAndClassifyAlert(alert.Labels, rules)
		if alertRuleId == "" {
			// as a fallback, compute from alert labels directly
			lbls := model.LabelSet{}
			for k, v := range alert.Labels {
				lbls[model.LabelName(k)] = model.LabelValue(v)
			}
			l, c := alertcomponent.DetermineComponent(lbls)
			if c == "" || c == "Others" {
				c = alert.Labels["namespace"]
				l = ""
			}
			component, layer = c, l
		}

		alert.AlertRuleId = alertRuleId
		alert.Component = component
		alert.Layer = layer
		result = append(result, alert)
	}

	return result, nil
}

// correlateAndClassifyAlert tries to find the base alert rule for the given alert labels
// by subset-matching against relabeled rules. If found, it computes the classification
// from the rule's labels and returns (id, component, layer).
func correlateAndClassifyAlert(alertLabels map[string]string, rules []monitoringv1.Rule) (string, string, string) {
	// Determine best match: prefer rules with more labels (more specific)
	var (
		bestId         string
		bestRule       *monitoringv1.Rule
		bestLabelCount int
	)
	for i := range rules {
		rule := &rules[i]
		ruleLabels := sanitizeRuleLabels(rule.Labels)
		if isSubset(ruleLabels, alertLabels) {
			if len(ruleLabels) > bestLabelCount {
				bestLabelCount = len(ruleLabels)
				bestRule = rule
				bestId = rule.Labels[k8s.AlertRuleLabelId]
			}
		}
	}
	if bestRule == nil {
		return "", "", ""
	}

	// Compute classification from rule labels (consistent with mapping)
	lbls := model.LabelSet{}
	for k, v := range bestRule.Labels {
		lbls[model.LabelName(k)] = model.LabelValue(v)
	}
	layer, component := alertcomponent.DetermineComponent(lbls)
	if component == "" || component == "Others" {
		component = bestRule.Labels[k8s.PrometheusRuleLabelNamespace]
		layer = ""
	}
	return bestId, component, layer
}

// sanitizeRuleLabels removes meta labels that will not be present on alerts
func sanitizeRuleLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if k == k8s.PrometheusRuleLabelNamespace || k == k8s.PrometheusRuleLabelName || k == k8s.AlertRuleLabelId {
			continue
		}
		out[k] = v
	}
	return out
}

// isSubset returns true if all key/value pairs in sub are present in sup
func isSubset(sub map[string]string, sup map[string]string) bool {
	for k, v := range sub {
		if sv, ok := sup[k]; !ok || sv != v {
			return false
		}
	}
	return true
}
