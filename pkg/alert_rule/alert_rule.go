package alertrule

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func GetAlertingRuleId(alertRule *monitoringv1.Rule) string {
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

	return fmt.Sprintf("%s;%x", name, hash)
}
