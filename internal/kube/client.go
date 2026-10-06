package kube

import (
	"os"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// RestConfig : configuration in-cluster quand l'application tourne dans un pod,
// sinon kubeconfig (--kubeconfig, $KUBECONFIG, ~/.kube/config) pour le dev local.
func RestConfig(kubeconfig, context string) (*rest.Config, error) {
	var (
		cfg *rest.Config
		err error
	)
	if kubeconfig == "" && context == "" && os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		cfg, err = rest.InClusterConfig()
	} else {
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		rules.ExplicitPath = kubeconfig
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: context}).ClientConfig()
	}
	if err != nil {
		return nil, err
	}
	cfg.UserAgent = "cluster-atlas"
	// Un watch par type et quelques lectures ponctuelles : de la marge sans saturer l'API server.
	cfg.QPS, cfg.Burst = 50, 100
	return cfg, nil
}
