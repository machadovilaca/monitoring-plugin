package k8s

type namespaceManager struct {
	namespaceInformer *namespaceInformer
}

func newNamespaceManager(informer *namespaceInformer) *namespaceManager {
	return &namespaceManager{
		namespaceInformer: informer,
	}
}

func (nm *namespaceManager) IsClusterMonitoringNamespace(name string) bool {
	return nm.namespaceInformer.isClusterMonitoringNamespace(name)
}
