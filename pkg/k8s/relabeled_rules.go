package k8s

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	alertrule "github.com/openshift/monitoring-plugin/pkg/alert_rule"
	"github.com/openshift/monitoring-plugin/pkg/alertcomponent"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringv1client "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/relabel"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const (
	resyncPeriod   = 15 * time.Minute
	queueBaseDelay = 50 * time.Millisecond
	queueMaxDelay  = 3 * time.Minute

	ClusterMonitoringNamespace = "openshift-monitoring"

	RelabeledRulesConfigMapName = "relabeled-rules-config"
	RelabeledRulesConfigMapKey  = "config.yaml"

	AlertRelabelConfigSecretName = "alert-relabel-configs"
	AlertRelabelConfigSecretKey  = "config.yaml"

	PrometheusRuleLabelNamespace = "openshift_io_prometheus_rule_namespace"
	PrometheusRuleLabelName      = "openshift_io_prometheus_rule_name"
	AlertRuleLabelId             = "openshift_io_alert_rule_id"

	AppKubernetesIoComponent                   = "app.kubernetes.io/component"
	AppKubernetesIoManagedBy                   = "app.kubernetes.io/managed-by"
	AppKubernetesIoComponentAlertManagementApi = "alert-management-api"
	AppKubernetesIoComponentMonitoringPlugin   = "monitoring-plugin"

	// Per-PrometheusRule alert rules classification map (component + layer)
	AlertRuleClassificationConfigMapNamePrefix = "alertrule-classification-"
	AlertRuleClassificationConfigMapKey        = "alert-rule-classification.yaml"

	// Additional labels for traceability and rename heuristics
	PrometheusRuleLabelUID          = "openshift_io_prometheus_rule_uid"
	ClassificationSignatureLabelKey = "openshift_io_classification_signature"
)

// alertRuleClassification represents the stored classification for a rule id
type alertRuleClassification struct {
	Component string `yaml:"component"`
	Layer     string `yaml:"layer"`
}

type relabeledRulesManager struct {
	queue workqueue.TypedRateLimitingInterface[string]

	namespaceManager        NamespaceInterface
	prometheusRulesInformer cache.SharedIndexInformer
	secretInformer          cache.SharedIndexInformer
	configMapInformer       cache.SharedIndexInformer
	clientset               kubernetes.Interface

	// relabeledRules stores the relabeled rules
	relabeledRules map[string]monitoringv1.Rule
	relabelConfigs []*relabel.Config
	mu             sync.RWMutex
}

func newRelabeledRulesManager(ctx context.Context, namespaceManager NamespaceInterface, monitoringv1clientset *monitoringv1client.Clientset, clientset *kubernetes.Clientset) (*relabeledRulesManager, error) {
	prometheusRulesInformer := cache.NewSharedIndexInformer(
		prometheusRuleListWatchForAllNamespaces(monitoringv1clientset),
		&monitoringv1.PrometheusRule{},
		resyncPeriod,
		cache.Indexers{},
	)

	secretInformer := cache.NewSharedIndexInformer(
		alertRelabelConfigSecretListWatch(clientset, ClusterMonitoringNamespace),
		&corev1.Secret{},
		resyncPeriod,
		cache.Indexers{},
	)

	configMapInformer := cache.NewSharedIndexInformer(
		configMapListWatch(clientset, ClusterMonitoringNamespace),
		&corev1.ConfigMap{},
		resyncPeriod,
		cache.Indexers{},
	)

	queue := workqueue.NewTypedRateLimitingQueueWithConfig(
		workqueue.NewTypedItemExponentialFailureRateLimiter[string](queueBaseDelay, queueMaxDelay),
		workqueue.TypedRateLimitingQueueConfig[string]{Name: "relabeled-rules"},
	)

	rrm := &relabeledRulesManager{
		queue:                   queue,
		namespaceManager:        namespaceManager,
		prometheusRulesInformer: prometheusRulesInformer,
		secretInformer:          secretInformer,
		configMapInformer:       configMapInformer,
		clientset:               clientset,
	}

	_, err := rrm.prometheusRulesInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			promRule, ok := obj.(*monitoringv1.PrometheusRule)
			if !ok {
				return
			}
			log.Debugf("prometheus rule added: %s/%s", promRule.Namespace, promRule.Name)
			rrm.queue.Add("prometheus-rule-sync")
		},
		UpdateFunc: func(oldObj interface{}, newObj interface{}) {
			promRule, ok := newObj.(*monitoringv1.PrometheusRule)
			if !ok {
				return
			}
			log.Debugf("prometheus rule updated: %s/%s", promRule.Namespace, promRule.Name)
			rrm.queue.Add("prometheus-rule-sync")
		},
		DeleteFunc: func(obj interface{}) {
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}

			promRule, ok := obj.(*monitoringv1.PrometheusRule)
			if !ok {
				return
			}
			log.Debugf("prometheus rule deleted: %s/%s", promRule.Namespace, promRule.Name)
			rrm.queue.Add("prometheus-rule-sync")
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add event handler to prometheus rules informer: %w", err)
	}

	_, err = rrm.secretInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			rrm.queue.Add("secret-sync")
		},
		UpdateFunc: func(oldObj interface{}, newObj interface{}) {
			rrm.queue.Add("secret-sync")
		},
		DeleteFunc: func(obj interface{}) {
			rrm.queue.Add("secret-sync")
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add event handler to secret informer: %w", err)
	}

	_, err = rrm.configMapInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			rrm.queue.Add("config-map-sync")
		},
		UpdateFunc: func(oldObj interface{}, newObj interface{}) {
			rrm.queue.Add("config-map-sync")
		},
		DeleteFunc: func(obj interface{}) {
			rrm.queue.Add("config-map-sync")
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add event handler to config map informer: %w", err)
	}

	go rrm.prometheusRulesInformer.Run(ctx.Done())
	go rrm.secretInformer.Run(ctx.Done())
	go rrm.configMapInformer.Run(ctx.Done())

	cache.WaitForNamedCacheSync("RelabeledRulesConfig informer", ctx.Done(),
		rrm.prometheusRulesInformer.HasSynced,
		rrm.secretInformer.HasSynced,
		rrm.configMapInformer.HasSynced,
	)

	go rrm.worker(ctx)
	rrm.queue.Add("initial-sync")

	return rrm, nil
}

func alertRelabelConfigSecretListWatch(clientset *kubernetes.Clientset, namespace string) *cache.ListWatch {
	return cache.NewListWatchFromClient(
		clientset.CoreV1().RESTClient(),
		"secrets",
		namespace,
		fields.OneTermEqualSelector("metadata.name", AlertRelabelConfigSecretName),
	)
}

func configMapListWatch(clientset *kubernetes.Clientset, namespace string) *cache.ListWatch {
	return cache.NewListWatchFromClient(
		clientset.CoreV1().RESTClient(),
		"configmaps",
		namespace,
		fields.OneTermEqualSelector("metadata.name", RelabeledRulesConfigMapName),
	)
}

func (rrm *relabeledRulesManager) worker(ctx context.Context) {
	for rrm.processNextWorkItem(ctx) {
	}
}

func (rrm *relabeledRulesManager) processNextWorkItem(ctx context.Context) bool {
	key, quit := rrm.queue.Get()
	if quit {
		return false
	}

	defer rrm.queue.Done(key)

	if err := rrm.sync(ctx, key); err != nil {
		log.Errorf("error syncing relabeled rules: %v", err)
		rrm.queue.AddRateLimited(key)
		return true
	}

	rrm.queue.Forget(key)

	return true
}

func (rrm *relabeledRulesManager) sync(ctx context.Context, key string) error {
	if key == "config-map-sync" {
		if err := rrm.reapplyConfigMap(ctx); err != nil {
			return err
		}
		return rrm.reconcileAlertComponentMaps(ctx)
	}

	relabelConfigs, err := rrm.loadRelabelConfigs()
	if err != nil {
		return fmt.Errorf("failed to load relabel configs: %w", err)
	}

	rrm.mu.Lock()
	rrm.relabelConfigs = relabelConfigs
	rrm.mu.Unlock()

	alerts := rrm.collectAlerts(relabelConfigs)

	rrm.mu.Lock()
	rrm.relabeledRules = alerts
	rrm.mu.Unlock()

	if err := rrm.reapplyConfigMap(ctx); err != nil {
		return err
	}
	return rrm.reconcileAlertComponentMaps(ctx)
}

func (rrm *relabeledRulesManager) reapplyConfigMap(ctx context.Context) error {
	rrm.mu.RLock()
	defer rrm.mu.RUnlock()

	data, err := yaml.Marshal(rrm.relabeledRules)
	if err != nil {
		return fmt.Errorf("failed to marshal relabeled rules: %w", err)
	}

	configMapData := map[string]string{
		RelabeledRulesConfigMapKey: string(data),
	}

	configMapClient := rrm.clientset.CoreV1().ConfigMaps(ClusterMonitoringNamespace)

	existingConfigMap, err := configMapClient.Get(ctx, RelabeledRulesConfigMapName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			log.Infof("Creating ConfigMap %s with %d relabeled rules", RelabeledRulesConfigMapName, len(rrm.relabeledRules))
			newConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      RelabeledRulesConfigMapName,
					Namespace: ClusterMonitoringNamespace,
					Labels: map[string]string{
						AppKubernetesIoManagedBy: AppKubernetesIoComponentMonitoringPlugin,
						AppKubernetesIoComponent: AppKubernetesIoComponentAlertManagementApi,
					},
				},
				Data: configMapData,
			}

			if _, err := configMapClient.Create(ctx, newConfigMap, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("failed to create config map: %w", err)
			}

			log.Infof("Successfully created ConfigMap %s", RelabeledRulesConfigMapName)
			return nil
		}

		return fmt.Errorf("failed to get config map: %w", err)
	}

	if existingConfigMap.Data[RelabeledRulesConfigMapKey] == configMapData[RelabeledRulesConfigMapKey] {
		log.Debugf("ConfigMap %s data unchanged, skipping update", RelabeledRulesConfigMapName)
		return nil
	}

	log.Infof("Updating ConfigMap %s with %d relabeled rules", RelabeledRulesConfigMapName, len(rrm.relabeledRules))
	existingConfigMap.Data = configMapData

	if _, err := configMapClient.Update(ctx, existingConfigMap, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update config map: %w", err)
	}

	log.Infof("Successfully updated ConfigMap %s", RelabeledRulesConfigMapName)
	return nil
}

func (rrm *relabeledRulesManager) loadRelabelConfigs() ([]*relabel.Config, error) {
	storeKey := fmt.Sprintf("%s/%s", ClusterMonitoringNamespace, AlertRelabelConfigSecretName)
	obj, exists, err := rrm.secretInformer.GetStore().GetByKey(storeKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret from store: %w", err)
	}
	if !exists {
		log.Infof("Alert relabel config secret %q not found", storeKey)
		return nil, nil
	}

	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil, fmt.Errorf("unexpected object type in secret store: %T", obj)
	}

	configData, ok := secret.Data[AlertRelabelConfigSecretKey]
	if !ok {
		return nil, fmt.Errorf("no config data found in secret %q", AlertRelabelConfigSecretName)
	}

	var configs []*relabel.Config
	if err := yaml.Unmarshal(configData, &configs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal relabel configs: %w", err)
	}

	for _, config := range configs {
		if config.NameValidationScheme == model.UnsetValidation {
			config.NameValidationScheme = model.UTF8Validation
		}
	}

	log.Infof("Loaded %d relabel configs from secret %s", len(configs), storeKey)
	return configs, nil
}

func (rrm *relabeledRulesManager) collectAlerts(relabelConfigs []*relabel.Config) map[string]monitoringv1.Rule {
	alerts := make(map[string]monitoringv1.Rule)

	for _, obj := range rrm.prometheusRulesInformer.GetStore().List() {
		promRule, ok := obj.(*monitoringv1.PrometheusRule)
		if !ok {
			continue
		}

		// Skip deleted rules
		if promRule.DeletionTimestamp != nil {
			continue
		}

		for _, group := range promRule.Spec.Groups {
			for _, rule := range group.Rules {
				// Only process alerting rules (skip recording rules)
				if rule.Alert == "" {
					continue
				}

				alertRuleId := alertrule.GetAlertingRuleId(&rule)

				if rule.Labels == nil {
					rule.Labels = make(map[string]string)
				}

				rule.Labels["alertname"] = rule.Alert

				if rrm.namespaceManager.IsClusterMonitoringNamespace(promRule.Namespace) {
					// Relabel the alert labels
					relabeledLabels, keep := relabel.Process(labels.FromMap(rule.Labels), relabelConfigs...)
					if !keep {
						// Alert was dropped by relabeling, skip it
						log.Infof("Skipping dropped alert %s from %s/%s", rule.Alert, promRule.Namespace, promRule.Name)
						continue
					}

					// Update the alert labels
					rule.Labels = relabeledLabels.Map()
				}

				rule.Labels[AlertRuleLabelId] = alertRuleId
				rule.Labels[PrometheusRuleLabelNamespace] = promRule.Namespace
				rule.Labels[PrometheusRuleLabelName] = promRule.Name

				alerts[alertRuleId] = rule
			}
		}
	}

	log.Debugf("Collected %d alerts", len(alerts))
	return alerts
}

func (rrm *relabeledRulesManager) List(ctx context.Context) []monitoringv1.Rule {
	rrm.mu.RLock()
	defer rrm.mu.RUnlock()

	var result []monitoringv1.Rule
	for _, rule := range rrm.relabeledRules {
		result = append(result, rule)
	}

	return result
}

func (rrm *relabeledRulesManager) Get(ctx context.Context, id string) (monitoringv1.Rule, bool) {
	rrm.mu.RLock()
	defer rrm.mu.RUnlock()

	rule, ok := rrm.relabeledRules[id]
	if !ok {
		return monitoringv1.Rule{}, false
	}

	return rule, true
}

func (rrm *relabeledRulesManager) Config() []*relabel.Config {
	rrm.mu.RLock()
	defer rrm.mu.RUnlock()

	return append([]*relabel.Config{}, rrm.relabelConfigs...)
}

// reconcileAlertComponentMaps builds and writes a per-PrometheusRule ConfigMap
// that maps alert rule IDs to their computed components.
func (rrm *relabeledRulesManager) reconcileAlertComponentMaps(ctx context.Context) error {
	for _, obj := range rrm.prometheusRulesInformer.GetStore().List() {
		promRule, ok := obj.(*monitoringv1.PrometheusRule)
		if !ok {
			continue
		}
		// Skip deleted rules
		if promRule.DeletionTimestamp != nil {
			continue
		}

		alertIdToClassification := make(map[string]alertRuleClassification)
		alertRulesIds := make([]string, 0, 16)

		for _, group := range promRule.Spec.Groups {
			for _, rule := range group.Rules {
				// Only process alerting rules
				if rule.Alert == "" {
					continue
				}
				// Build label set used for component determination
				lbls := model.LabelSet{}
				for k, v := range rule.Labels {
					lbls[model.LabelName(k)] = model.LabelValue(v)
				}
				lbls["alertname"] = model.LabelValue(rule.Alert)

				// Compute component using CHA-compatible logic
				layer, component := alertcomponent.DetermineComponent(lbls)
				if component == "" || component == "Others" {
					// Fallback to PR namespace when unknown; layer intentionally left empty
					component = promRule.Namespace
					layer = ""
				}

				alertRuleId := alertrule.GetAlertingRuleId(&rule)
				alertIdToClassification[alertRuleId] = alertRuleClassification{
					Component: component,
					Layer:     layer,
				}
				alertRulesIds = append(alertRulesIds, alertRuleId)
			}
		}

		// Write/update the ConfigMap in the same namespace as the PrometheusRule
		if err := rrm.applyAlertRuleClassificationConfigMap(ctx, promRule, alertIdToClassification, alertRulesIds); err != nil {
			return err
		}
	}
	return nil
}

func (rrm *relabeledRulesManager) applyAlertRuleClassificationConfigMap(ctx context.Context, promRule *monitoringv1.PrometheusRule, generated map[string]alertRuleClassification, alertRuleIds []string) error {
	configMapClient := rrm.clientset.CoreV1().ConfigMaps(promRule.Namespace)
	cmName := AlertRuleClassificationConfigMapNamePrefix + promRule.Name

	// Compute stable signature label based on sorted alertRuleIds
	signature := computeClassificationSignature(alertRuleIds)

	// Start from generated data, then merge user overrides from existing ConfigMap (if any)
	merged := make(map[string]alertRuleClassification, len(generated))
	for k, v := range generated {
		merged[k] = v
	}

	// Attempt to load existing ConfigMap to preserve user overrides
	existing, err := configMapClient.Get(ctx, cmName, metav1.GetOptions{})
	if err == nil {
		existingPayload := existing.Data[AlertRuleClassificationConfigMapKey]
		if existingPayload != "" {
			var existingMap map[string]alertRuleClassification
			if err := yaml.Unmarshal([]byte(existingPayload), &existingMap); err != nil {
				log.Warnf("failed to unmarshal existing classification for %s/%s: %v", promRule.Namespace, cmName, err)
			} else {
				// For each id that still exists in generated set, if user provided non-empty override, validate and apply
				for id, userVal := range existingMap {
					if _, ok := merged[id]; !ok {
						// ignore unknown ids (likely removed rules)
						continue
					}
					if userVal.Component != "" {
						if validateComponent(userVal.Component) {
							mv := merged[id]
							mv.Component = userVal.Component
							merged[id] = mv
						} else {
							log.Warnf("invalid component override for %s in %s/%s: %q", id, promRule.Namespace, cmName, userVal.Component)
						}
					}
					// layer can be "", or one of allowed values
					if userVal.Layer != "" {
						if validateLayer(userVal.Layer) {
							mv := merged[id]
							mv.Layer = userVal.Layer
							merged[id] = mv
						} else {
							log.Warnf("invalid layer override for %s in %s/%s: %q", id, promRule.Namespace, cmName, userVal.Layer)
						}
					}
				}
			}
		}
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get ConfigMap %s/%s: %w", promRule.Namespace, cmName, err)
	}

	yamlData, err := yaml.Marshal(merged)
	if err != nil {
		return fmt.Errorf("failed to marshal alert components map for %s/%s: %w", promRule.Namespace, promRule.Name, err)
	}

	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: promRule.Namespace,
			Labels: map[string]string{
				AppKubernetesIoManagedBy:        AppKubernetesIoComponentMonitoringPlugin,
				AppKubernetesIoComponent:        AppKubernetesIoComponentAlertManagementApi,
				PrometheusRuleLabelNamespace:    promRule.Namespace,
				PrometheusRuleLabelName:         promRule.Name,
				PrometheusRuleLabelUID:          string(promRule.UID),
				ClassificationSignatureLabelKey: signature,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: monitoringv1.SchemeGroupVersion.String(),
					Kind:       "PrometheusRule",
					Name:       promRule.Name,
					UID:        promRule.UID,
					Controller: boolPtr(true),
				},
			},
		},
		Data: map[string]string{
			AlertRuleClassificationConfigMapKey: string(yamlData),
		},
	}

	existing, err = configMapClient.Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			if _, err := configMapClient.Create(ctx, desired, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("failed to create ConfigMap %s/%s: %w", promRule.Namespace, cmName, err)
			}
			log.Infof("Created ConfigMap %s/%s with alert components map", promRule.Namespace, cmName)
			return nil
		}
		return fmt.Errorf("failed to get ConfigMap %s/%s: %w", promRule.Namespace, cmName, err)
	}

	// Update if data differs
	if existing.Data == nil {
		existing.Data = make(map[string]string)
	}
	if existing.Data[AlertRuleClassificationConfigMapKey] == string(yamlData) {
		return nil
	}
	existing.Data[AlertRuleClassificationConfigMapKey] = string(yamlData)
	// Merge labels, preserving any external labels and updating our managed ones
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	existing.Labels[AppKubernetesIoManagedBy] = AppKubernetesIoComponentMonitoringPlugin
	existing.Labels[AppKubernetesIoComponent] = AppKubernetesIoComponentAlertManagementApi
	existing.Labels[PrometheusRuleLabelNamespace] = promRule.Namespace
	existing.Labels[PrometheusRuleLabelName] = promRule.Name
	existing.Labels[PrometheusRuleLabelUID] = string(promRule.UID)
	existing.Labels[ClassificationSignatureLabelKey] = signature
	existing.OwnerReferences = desired.OwnerReferences

	if _, err := configMapClient.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to update ConfigMap %s/%s: %w", promRule.Namespace, cmName, err)
	}
	log.Infof("Updated ConfigMap %s/%s with alert components map", promRule.Namespace, cmName)
	return nil
}

func boolPtr(b bool) *bool {
	return &b
}

// computeClassificationSignature returns a stable signature for a set of alertRuleIds.
// It sorts the ids and returns a hex-encoded sha256 hash of the joined list.
func computeClassificationSignature(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	cp := make([]string, len(ids))
	copy(cp, ids)
	sort.Strings(cp)
	h := sha256.Sum256([]byte(strings.Join(cp, "\n")))
	return fmt.Sprintf("%x", h[:])
}

var componentRe = regexp.MustCompile(`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`)

func validateComponent(s string) bool {
	if len(s) == 0 {
		return false
	}
	// allow up to 253 chars similar to DNS subdomain length; keep basic safety
	if len(s) > 253 {
		return false
	}
	return componentRe.MatchString(s)
}

func validateLayer(s string) bool {
	switch s {
	case "cluster", "compute", "namespace", "":
		return true
	default:
		return false
	}
}
