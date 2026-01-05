package alertcomponent

import (
	"regexp"

	"github.com/prometheus/common/model"
)

// LabelsMatcher represents a matcher definition for a set of labels.
// It matches if all of the label matchers match the labels.
type LabelsMatcher interface {
	Matches(labels model.LabelSet) (match bool, keys []model.LabelName)
	Equals(other LabelsMatcher) bool
}

func NewLabelsMatcher(key string, matcher ValueMatcher) LabelsMatcher {
	return labelMatcher{key: key, matcher: matcher}
}

func NewStringValuesMatcher(keys ...string) ValueMatcher {
	return stringMatcher(keys)
}

func NewRegexValuesMatcher(regexes ...*regexp.Regexp) ValueMatcher {
	return regexpMatcher(regexes)
}

// labelMatcher represents a matcher definition for a label.
type labelMatcher struct {
	key     string
	matcher ValueMatcher
}

// Matches implements the LabelsMatcher interface.
func (l labelMatcher) Matches(labels model.LabelSet) (bool, []model.LabelName) {
	if l.matcher.Matches(string(labels[model.LabelName(l.key)])) {
		return true, []model.LabelName{model.LabelName(l.key)}
	}
	return false, nil
}

// Equals implements the LabelsMatcher interface.
func (l labelMatcher) Equals(other LabelsMatcher) bool {
	ol, ok := other.(labelMatcher)
	if !ok {
		return false
	}
	return l.key == ol.key && l.matcher.Equals(ol.matcher)
}

// ValueMatcher represents a matcher for a specific value.
//
// Multiple implementations are provided for different types of matchers.
type ValueMatcher interface {
	Matches(value string) bool
	Equals(other ValueMatcher) bool
}

// stringMatcher is a matcher for a list of strings.
//
// It matches if the value is in the list of strings.
type stringMatcher []string

func (s stringMatcher) Matches(value string) bool {
	for _, v := range s {
		if v == value {
			return true
		}
	}
	return false
}

// Equals implements the ValueMatcher interface.
func (s stringMatcher) Equals(other ValueMatcher) bool {
	o, ok := other.(stringMatcher)
	if !ok {
		return false
	}
	return equalsNoOrder(s, o)
}

// regexpMatcher is a matcher for a list of regular expressions.
//
// It matches if the value matches any of the regular expressions.
type regexpMatcher []*regexp.Regexp

func (r regexpMatcher) Matches(value string) bool {
	for _, re := range r {
		if re.MatchString(value) {
			return true
		}
	}
	return false
}

// Equals implements the ValueMatcher interface.
func (r regexpMatcher) Equals(other ValueMatcher) bool {
	o, ok := other.(regexpMatcher)
	if !ok {
		return false
	}
	s1 := make([]string, 0, len(r))
	for _, re := range r {
		s1 = append(s1, re.String())
	}
	s2 := make([]string, 0, len(o))
	for _, re := range o {
		s2 = append(s2, re.String())
	}
	return equalsNoOrder(s1, s2)
}

func equalsNoOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	seen := make(map[string]int, len(a))
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		if seen[v] == 0 {
			return false
		}
		seen[v]--
	}
	return true
}

// componentMatcher represents a matcher definition for a component.
//
// It matches if any of the label matchers match the labels.
type componentMatcher struct {
	component string
	matchers  []LabelsMatcher
}

// findComponent tries to determine a component for given labels using the provided matchers.
//
// It returns the component and the keys that matched.
// If no match is found, it returns an empty component and nil keys.
func findComponent(compMatchers []componentMatcher, labels model.LabelSet) (
	component string, keys []model.LabelName) {
	for _, compMatcher := range compMatchers {
		for _, labelsMatcher := range compMatcher.matchers {
			if matches, keys := labelsMatcher.Matches(labels); matches {
				return compMatcher.component, keys
			}
		}
	}
	return "", nil
}

// componentMatcherFn is a function that tries matching provided labels to a component.
// It returns the layer, component and the keys from the labels that were used for matching.
// If no match is found, it returns an empty layer, component and nil keys.
type componentMatcherFn func(labels model.LabelSet) (layer, comp model.LabelValue, keys []model.LabelName)

func evalMatcherFns(fns []componentMatcherFn, labels model.LabelSet) (
	layer, comp string, labelsSubset model.LabelSet) {
	for _, fn := range fns {
		if layer, comp, keys := fn(labels); layer != "" {
			return string(layer), string(comp), getLabelsSubset(labels, keys...)
		}
	}
	return "Others", "Others", getLabelsSubset(labels)
}

// getLabelsSubset returns a subset of the labels with given keys.
func getLabelsSubset(m model.LabelSet, keys ...model.LabelName) model.LabelSet {
	keys = append([]model.LabelName{"namespace", "alertname", "severity"}, keys...)
	return getMapSubset(m, keys...)
}

// getMapSubset returns a subset of the labels with given keys.
func getMapSubset(m model.LabelSet, keys ...model.LabelName) model.LabelSet {
	subset := make(model.LabelSet, len(keys))
	for _, key := range keys {
		if val, ok := m[key]; ok {
			subset[key] = val
		}
	}
	return subset
}

var (
	nodeAlerts []model.LabelValue = []model.LabelValue{
		"NodeClockNotSynchronising",
		"KubeNodeNotReady",
		"KubeNodeUnreachable",
		"NodeSystemSaturation",
		"NodeFilesystemSpaceFillingUp",
		"NodeFilesystemAlmostOutOfSpace",
		"NodeMemoryMajorPagesFaults",
		"NodeNetworkTransmitErrs",
		"NodeTextFileCollectorScrapeError",
		"NodeFilesystemFilesFillingUp",
		"NodeNetworkReceiveErrs",
		"NodeClockSkewDetected",
		"NodeFilesystemAlmostOutOfFiles",
		"NodeWithoutOVNKubeNodePodRunning",
		"InfraNodesNeedResizingSRE",
		"NodeHighNumberConntrackEntriesUsed",
		"NodeMemHigh",
		"NodeNetworkInterfaceFlapping",
		"NodeWithoutSDNPod",
		"NodeCpuHigh",
		"CriticalNodeNotReady",
		"NodeFileDescriptorLimit",
		"MCCPoolAlert",
		"MCCDrainError",
		"MCDRebootError",
		"MCDPivotError",
	}

	coreMatchers = []componentMatcher{
		{"etcd", []LabelsMatcher{
			NewLabelsMatcher("namespace",
				NewStringValuesMatcher(
					"openshift-etcd",
					"openshift-etcd-operator"))}},
		{"kube-apiserver", []LabelsMatcher{
			NewLabelsMatcher("namespace",
				NewStringValuesMatcher(
					"openshift-kube-apiserver",
					"openshift-kube-apiserver-operator"))}},
		{"kube-controller-manager", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-kube-controller-manager",
				"openshift-kube-controller-manager-operator",
				"kube-system",
			))}},
		{"kube-scheduler", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-kube-scheduler",
				"openshift-kube-scheduler-operator",
			))}},
		{"machine-approver", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-cluster-machine-approver",
				"openshift-machine-approver-operator",
			))}},
		{"machine-config", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-machine-config-operator",
			)),
			NewLabelsMatcher("alertname", NewStringValuesMatcher(
				"HighOverallControlPlaneMemory",
				"ExtremelyHighIndividualControlPlaneMemory",
				"MissingMachineConfig",
				"MCCBootImageUpdateError",
				"KubeletHealthState",
				"SystemMemoryExceedsReservation",
			))}},
		{"version", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-cluster-version",
				"openshift-version-operator",
			)),
			NewLabelsMatcher("alertname", NewStringValuesMatcher(
				"ClusterNotUpgradeable",
				"UpdateAvailable",
			))}},
		{"dns", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-dns",
				"openshift-dns-operator",
			))}},
		{"authentication", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-authentication",
				"openshift-oauth-apiserver",
				"openshift-authentication-operator",
			))}},
		{"cert-manager", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-cert-manager",
				"openshift-cert-manager-operator",
			))}},
		{"cloud-controller-manager", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-cloud-controller-manager",
				"openshift-cloud-controller-manager-operator",
			))}},
		{"cloud-credential", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-cloud-credential-operator",
			))}},
		{"cluster-api", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-cluster-api",
				"openshift-cluster-api-operator",
			))}},
		{"config-operator", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-config-operator",
			))}},
		{"kube-storage-version-migrator", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-kube-storage-version-migrator",
				"openshift-kube-storage-version-migrator-operator",
			))}},
		{"image-registry", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-image-registry",
				"openshift-image-registry-operator",
			))}},
		{"ingress", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-ingress",
				"openshift-route-controller-manager",
				"openshift-ingress-canary",
				"openshift-ingress-operator",
			))}},
		{"console", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-console",
				"openshift-console-operator",
			))}},
		{"insights", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-insights",
				"openshift-insights-operator",
			))}},
		{"machine-api", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-machine-api",
				"openshift-machine-api-operator",
			))}},
		{"monitoring", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-monitoring",
				"openshift-monitoring-operator",
			))}},
		{"network", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-network-operator",
				"openshift-ovn-kubernetes",
				"openshift-multus",
				"openshift-network-diagnostics",
				"openshift-sdn",
			))}},
		{"node-tuning", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-cluster-node-tuning-operator",
				"openshift-node-tuning-operator",
			))}},
		{"openshift-apiserver", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-apiserver",
				"openshift-apiserver-operator",
			))}},
		{"openshift-controller-manager", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-controller-manager",
				"openshift-controller-manager-operator",
			))}},
		{"openshift-samples", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-cluster-samples-operator",
				"openshift-samples-operator",
			))}},
		{"operator-lifecycle-manager", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-operator-lifecycle-manager",
			))}},
		{"service-ca", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-service-ca",
				"openshift-service-ca-operator",
			))}},
		{"storage", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-storage",
				"openshift-cluster-csi-drivers",
				"openshift-cluster-storage-operator",
				"openshift-storage-operator",
			))}},
		{"vertical-pod-autoscaler", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-vertical-pod-autoscaler",
				"openshift-vertical-pod-autoscaler-operator",
			))}},
		{"marketplace", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-marketplace",
				"openshift-marketplace-operator",
			)),
		}},
	}

	workloadMatchers = []componentMatcher{
		{"openshift-compliance", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-compliance",
			))}},
		{"openshift-file-integrity", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-file-integrity",
			))}},
		{"openshift-logging", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-logging",
			))}},
		{"openshift-user-workload-monitoring", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-user-workload-monitoring",
			))}},
		{"openshift-gitops", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-gitops",
				"openshift-gitops-operator",
			))}},
		{"openshift-operators", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-operators",
			))}},
		{"kubevirt", []LabelsMatcher{
			NewLabelsMatcher("kubernetes_operator_part_of", NewStringValuesMatcher(
				"kubevirt",
			)),
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-cnv",
			)),
		}},
		{"openshift-local-storage", []LabelsMatcher{
			NewLabelsMatcher("namespace", NewStringValuesMatcher(
				"openshift-local-storage",
			))}},
		{"quay", []LabelsMatcher{
			NewLabelsMatcher("container", NewStringValuesMatcher(
				"quay-app",
				"quay-mirror",
				"quay-app-upgrade",
			))}},
		{"Argo", []LabelsMatcher{
			NewLabelsMatcher("alertname", NewRegexValuesMatcher(
				regexp.MustCompile("^Argo"),
			))},
		},
	}
)

var cvoAlerts = []model.LabelValue{"ClusterOperatorDown", "ClusterOperatorDegraded"}

func cvoAlertsMatcher(labels model.LabelSet) (layer, comp model.LabelValue, keys []model.LabelName) {
	for _, v := range cvoAlerts {
		if labels["alertname"] == v {
			component := labels["name"]
			if component == "" {
				component = "version"
			}
			return "cluster", component, nil
		}
	}
	return "", "", nil
}

func computeMatcher(labels model.LabelSet) (layer, comp model.LabelValue, keys []model.LabelName) {
	for _, nodeAlert := range nodeAlerts {
		if labels["alertname"] == nodeAlert {
			component := "compute"
			return "compute", model.LabelValue(component), nil
		}
	}
	return "", "", nil
}

func coreMatcher(labels model.LabelSet) (layer, comp model.LabelValue, keys []model.LabelName) {
	// Try matching against core components.
	if component, keys := findComponent(coreMatchers, labels); component != "" {
		return "cluster", model.LabelValue(component), keys
	}
	return "", "", nil
}

func workloadMatcher(labels model.LabelSet) (layer, comp model.LabelValue, keys []model.LabelName) {
	// Try matching against workload components.
	if component, keys := findComponent(workloadMatchers, labels); component != "" {
		return "namespace", model.LabelValue(component), keys
	}
	return "", "", nil
}

// DetermineComponent determines the component for a given set of labels.
// It returns the layer and component strings.
func DetermineComponent(labels model.LabelSet) (layer, component string) {
	layer, component, _ = evalMatcherFns([]componentMatcherFn{
		cvoAlertsMatcher,
		computeMatcher,
		coreMatcher,
		workloadMatcher,
	}, labels)
	return layer, component
}
