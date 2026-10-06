package access

import (
	"sync"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ClientSource fournit les clients avec lesquels agir pour un utilisateur.
type ClientSource interface {
	Kube(u User) (kubernetes.Interface, error)
	Dynamic(u User) (dynamic.Interface, error)
	// Config : configuration REST (pour l'exec, qui ouvre ses propres connexions).
	Config(u User) (*rest.Config, error)
}

// Clients fournit, par utilisateur, des clients Kubernetes qui envoient
// Impersonate-User et Impersonate-Group : logs, exec et actions partent
// toujours au nom de l'utilisateur, jamais avec les droits du ServiceAccount.
type Clients struct {
	base *rest.Config

	mu      sync.Mutex
	clients map[string]userClients
}

type userClients struct {
	kube kubernetes.Interface
	dyn  dynamic.Interface
}

func NewClients(base *rest.Config) *Clients {
	return &Clients{base: base, clients: map[string]userClients{}}
}

func (c *Clients) Kube(u User) (kubernetes.Interface, error) {
	uc, err := c.get(u)
	return uc.kube, err
}

func (c *Clients) Dynamic(u User) (dynamic.Interface, error) {
	uc, err := c.get(u)
	return uc.dyn, err
}

func (c *Clients) Config(u User) (*rest.Config, error) { return c.configFor(u), nil }

func (c *Clients) get(u User) (userClients, error) {
	k := u.key()
	c.mu.Lock()
	defer c.mu.Unlock()
	if uc, ok := c.clients[k]; ok {
		return uc, nil
	}
	cfg := c.configFor(u)
	kube, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return userClients{}, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return userClients{}, err
	}
	// Borne simple : au-delà de 1 000 identités, on repart de zéro.
	if len(c.clients) >= 1000 {
		c.clients = map[string]userClients{}
	}
	uc := userClients{kube: kube, dyn: dyn}
	c.clients[k] = uc
	return uc, nil
}

func (c *Clients) configFor(u User) *rest.Config {
	cfg := rest.CopyConfig(c.base)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: u.Name, Groups: u.Groups}
	return cfg
}

// Static renvoie toujours les mêmes clients : mode sans authentification
// (droits du kubeconfig ou du ServiceAccount).
type Static struct {
	K kubernetes.Interface
	D dynamic.Interface
	C *rest.Config
}

func (s Static) Kube(User) (kubernetes.Interface, error) { return s.K, nil }
func (s Static) Dynamic(User) (dynamic.Interface, error) { return s.D, nil }
func (s Static) Config(User) (*rest.Config, error)       { return s.C, nil }
