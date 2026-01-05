package k8s

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	alertrule "github.com/openshift/monitoring-plugin/pkg/alert_rule"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/prometheus/model/relabel"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

var _ = Describe("Relabeled rules classification (Ginkgo)", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("falls back to namespace and empty layer when unknown", func() {
		k8sClient := fake.NewSimpleClientset()
		pr := &monitoringv1.PrometheusRule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-alerts",
				Namespace: "ns-a",
				UID:       "12345",
			},
			Spec: monitoringv1.PrometheusRuleSpec{
				Groups: []monitoringv1.RuleGroup{
					{
						Name: "group1",
						Rules: []monitoringv1.Rule{
							{Alert: "MyAppErrorRate", Labels: map[string]string{}},
						},
					},
				},
			},
		}
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
					return &monitoringv1.PrometheusRuleList{}, nil
				},
				WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
					return watch.NewEmptyWatch(), nil
				},
			},
			&monitoringv1.PrometheusRule{},
			0,
			cache.Indexers{},
		)
		Expect(informer.GetStore().Add(pr)).To(Succeed())
		rrm := &relabeledRulesManager{
			prometheusRulesInformer: informer,
			clientset:               k8sClient,
			relabeledRules:          map[string]monitoringv1.Rule{},
			relabelConfigs:          []*relabel.Config{},
		}
		Expect(rrm.reconcileAlertComponentMaps(ctx)).To(Succeed())
		cmName := AlertRuleClassificationConfigMapNamePrefix + pr.Name
		cm, err := k8sClient.CoreV1().ConfigMaps(pr.Namespace).Get(ctx, cmName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		var out map[string]struct {
			Component string `yaml:"component"`
			Layer     string `yaml:"layer"`
		}
		Expect(yaml.Unmarshal([]byte(cm.Data[AlertRuleClassificationConfigMapKey]), &out)).To(Succeed())
		id := alertrule.GetAlertingRuleId(&pr.Spec.Groups[0].Rules[0])
		cl, ok := out[id]
		Expect(ok).To(BeTrue())
		Expect(cl.Component).To(Equal(pr.Namespace))
		Expect(cl.Layer).To(Equal(""))
	})

	It("merges valid user overrides and ignores unknown IDs", func() {
		k8sClient := fake.NewSimpleClientset()
		pr := &monitoringv1.PrometheusRule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "platform-alerts",
				Namespace: "openshift-monitoring",
				UID:       "abcde",
			},
			Spec: monitoringv1.PrometheusRuleSpec{
				Groups: []monitoringv1.RuleGroup{
					{
						Name: "cluster",
						Rules: []monitoringv1.Rule{
							{Alert: "HighLatency", Labels: map[string]string{"namespace": "openshift-kube-apiserver"}},
						},
					},
				},
			},
		}
		id := alertrule.GetAlertingRuleId(&pr.Spec.Groups[0].Rules[0])
		override := map[string]map[string]string{
			id:           {"component": "custom-comp", "layer": "namespace"},
			"unknown-id": {"component": "should-be-ignored", "layer": "cluster"},
		}
		payload, _ := yaml.Marshal(override)
		cmName := AlertRuleClassificationConfigMapNamePrefix + pr.Name
		_, err := k8sClient.CoreV1().ConfigMaps(pr.Namespace).Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: pr.Namespace},
			Data:       map[string]string{AlertRuleClassificationConfigMapKey: string(payload)},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
					return &monitoringv1.PrometheusRuleList{}, nil
				},
				WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
					return watch.NewEmptyWatch(), nil
				},
			},
			&monitoringv1.PrometheusRule{},
			0,
			cache.Indexers{},
		)
		Expect(informer.GetStore().Add(pr)).To(Succeed())
		rrm := &relabeledRulesManager{
			prometheusRulesInformer: informer,
			clientset:               k8sClient,
			relabeledRules:          map[string]monitoringv1.Rule{},
			relabelConfigs:          []*relabel.Config{},
		}
		Expect(rrm.reconcileAlertComponentMaps(ctx)).To(Succeed())
		cm, err := k8sClient.CoreV1().ConfigMaps(pr.Namespace).Get(ctx, cmName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		var out map[string]struct {
			Component string `yaml:"component"`
			Layer     string `yaml:"layer"`
		}
		Expect(yaml.Unmarshal([]byte(cm.Data[AlertRuleClassificationConfigMapKey]), &out)).To(Succeed())
		cl, ok := out[id]
		Expect(ok).To(BeTrue())
		Expect(cl.Component).To(Equal("custom-comp"))
		Expect(cl.Layer).To(Equal("namespace"))
		_, present := out["unknown-id"]
		Expect(present).To(BeFalse())
	})

	It("maps kube-apiserver to component kube-apiserver and layer cluster", func() {
		k8sClient := fake.NewSimpleClientset()
		pr := &monitoringv1.PrometheusRule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "platform-alerts",
				Namespace: "openshift-monitoring",
				UID:       "abcde",
			},
			Spec: monitoringv1.PrometheusRuleSpec{
				Groups: []monitoringv1.RuleGroup{
					{
						Name: "cluster",
						Rules: []monitoringv1.Rule{
							{Alert: "HighLatency", Labels: map[string]string{"namespace": "openshift-kube-apiserver"}},
						},
					},
				},
			},
		}
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
					return &monitoringv1.PrometheusRuleList{}, nil
				},
				WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
					return watch.NewEmptyWatch(), nil
				},
			},
			&monitoringv1.PrometheusRule{},
			0,
			cache.Indexers{},
		)
		Expect(informer.GetStore().Add(pr)).To(Succeed())
		rrm := &relabeledRulesManager{
			prometheusRulesInformer: informer,
			clientset:               k8sClient,
			relabeledRules:          map[string]monitoringv1.Rule{},
			relabelConfigs:          []*relabel.Config{},
		}
		Expect(rrm.reconcileAlertComponentMaps(ctx)).To(Succeed())
		cmName := AlertRuleClassificationConfigMapNamePrefix + pr.Name
		cm, err := k8sClient.CoreV1().ConfigMaps(pr.Namespace).Get(ctx, cmName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		var out map[string]struct {
			Component string `yaml:"component"`
			Layer     string `yaml:"layer"`
		}
		Expect(yaml.Unmarshal([]byte(cm.Data[AlertRuleClassificationConfigMapKey]), &out)).To(Succeed())
		id := alertrule.GetAlertingRuleId(&pr.Spec.Groups[0].Rules[0])
		cl, ok := out[id]
		Expect(ok).To(BeTrue())
		Expect(cl.Component).To(Equal("kube-apiserver"))
		Expect(cl.Layer).To(Equal("cluster"))
	})

	It("ignores invalid user overrides and keeps generated values", func() {
		k8sClient := fake.NewSimpleClientset()
		pr := &monitoringv1.PrometheusRule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "platform-alerts",
				Namespace: "openshift-monitoring",
				UID:       "abcde",
			},
			Spec: monitoringv1.PrometheusRuleSpec{
				Groups: []monitoringv1.RuleGroup{
					{
						Name: "cluster",
						Rules: []monitoringv1.Rule{
							{Alert: "HighLatency", Labels: map[string]string{"namespace": "openshift-kube-apiserver"}},
						},
					},
				},
			},
		}
		id := alertrule.GetAlertingRuleId(&pr.Spec.Groups[0].Rules[0])
		override := map[string]map[string]string{
			id: {"component": "!!bad!!", "layer": "wrong"},
		}
		payload, _ := yaml.Marshal(override)
		cmName := AlertRuleClassificationConfigMapNamePrefix + pr.Name
		_, err := k8sClient.CoreV1().ConfigMaps(pr.Namespace).Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: pr.Namespace},
			Data:       map[string]string{AlertRuleClassificationConfigMapKey: string(payload)},
		}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())
		informer := cache.NewSharedIndexInformer(
			&cache.ListWatch{
				ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
					return &monitoringv1.PrometheusRuleList{}, nil
				},
				WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
					return watch.NewEmptyWatch(), nil
				},
			},
			&monitoringv1.PrometheusRule{},
			0,
			cache.Indexers{},
		)
		Expect(informer.GetStore().Add(pr)).To(Succeed())
		rrm := &relabeledRulesManager{
			prometheusRulesInformer: informer,
			clientset:               k8sClient,
			relabeledRules:          map[string]monitoringv1.Rule{},
			relabelConfigs:          []*relabel.Config{},
		}
		Expect(rrm.reconcileAlertComponentMaps(ctx)).To(Succeed())
		cm, err := k8sClient.CoreV1().ConfigMaps(pr.Namespace).Get(ctx, cmName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		var out map[string]struct {
			Component string `yaml:"component"`
			Layer     string `yaml:"layer"`
		}
		Expect(yaml.Unmarshal([]byte(cm.Data[AlertRuleClassificationConfigMapKey]), &out)).To(Succeed())
		cl, ok := out[id]
		Expect(ok).To(BeTrue())
		Expect(cl.Component).To(Equal("kube-apiserver"))
		Expect(cl.Layer).To(Equal("cluster"))
	})
})

