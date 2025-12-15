package mapper

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"sync"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/prometheus/model/relabel"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"

	"github.com/openshift/monitoring-plugin/pkg/k8s"
)

type mapper struct {
	k8sClient k8s.Client
	mu        sync.RWMutex

	prometheusRules     map[PrometheusRuleId][]PrometheusAlertRuleId
	alertRelabelConfigs []*relabel.Config
}

var _ Client = (*mapper)(nil)

func (m *mapper) GetAlertingRuleId(alertRule *monitoringv1.Rule) PrometheusAlertRuleId {
	var kind, name string
	if alertRule.Alert != "" {
		kind = "alert"
		name = alertRule.Alert
	} else if alertRule.Record != "" {
		kind = "record"
		name = alertRule.Record
	} else {
		return ""
	}

	expr := alertRule.Expr.String()
	forDuration := ""
	if alertRule.For != nil {
		forDuration = string(*alertRule.For)
	}

	var sortedLabels []string
	if alertRule.Labels != nil {
		for key, value := range alertRule.Labels {
			sortedLabels = append(sortedLabels, fmt.Sprintf("%s=%s", key, value))
		}
		sort.Strings(sortedLabels)
	}

	var sortedAnnotations []string
	if alertRule.Annotations != nil {
		for key, value := range alertRule.Annotations {
			sortedAnnotations = append(sortedAnnotations, fmt.Sprintf("%s=%s", key, value))
		}
		sort.Strings(sortedAnnotations)
	}

	// Build the hash input string
	hashInput := strings.Join([]string{
		kind,
		name,
		expr,
		forDuration,
		strings.Join(sortedLabels, ","),
		strings.Join(sortedAnnotations, ","),
	}, "\n")

	// Generate SHA256 hash
	hash := sha256.Sum256([]byte(hashInput))

	return PrometheusAlertRuleId(fmt.Sprintf("%s/%x", name, hash))
}

func (m *mapper) FindAlertRuleById(alertRuleId PrometheusAlertRuleId) (*PrometheusRuleId, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for id, rules := range m.prometheusRules {
		if slices.Contains(rules, alertRuleId) {
			return &id, nil
		}
	}

	// If the PrometheusRuleId is not found, return an error
	return nil, fmt.Errorf("alert rule with id %s not found", alertRuleId)
}

func (m *mapper) WatchPrometheusRules(ctx context.Context) {
	go func() {
		callbacks := k8s.PrometheusRuleInformerCallback{
			OnAdd: func(pr *monitoringv1.PrometheusRule) {
				m.AddPrometheusRule(pr)
			},
			OnUpdate: func(pr *monitoringv1.PrometheusRule) {
				m.AddPrometheusRule(pr)
			},
			OnDelete: func(key cache.ObjectName) {
				m.DeletePrometheusRule(key)
			},
		}

		err := m.k8sClient.PrometheusRuleInformer().AddCallbacks(callbacks)
		if err != nil {
			log.Fatalf("Failed to add PrometheusRule callbacks: %v", err)
		}
	}()
}

func (m *mapper) AddPrometheusRule(pr *monitoringv1.PrometheusRule) {
	m.mu.Lock()
	defer m.mu.Unlock()

	promRuleId := PrometheusRuleId(types.NamespacedName{Namespace: pr.Namespace, Name: pr.Name})
	delete(m.prometheusRules, promRuleId)

	rules := make([]PrometheusAlertRuleId, 0)
	for _, group := range pr.Spec.Groups {
		for _, rule := range group.Rules {
			if rule.Alert != "" {
				ruleId := m.GetAlertingRuleId(&rule)
				if ruleId != "" {
					rules = append(rules, ruleId)
				}
			}
		}
	}

	m.prometheusRules[promRuleId] = rules
}

func (m *mapper) DeletePrometheusRule(key cache.ObjectName) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.prometheusRules, PrometheusRuleId(key))
}
