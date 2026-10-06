package access

import (
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Clients fournit, par utilisateur, un client Kubernetes qui envoie
// Impersonate-User et Impersonate-Group : logs, exec et actions partent
// toujours au nom de l'utilisateur, jamais avec les droits du ServiceAccount.
type Clients struct {
	base *rest.Config

	mu      sync.Mutex
	clients map[string]kubernetes.Interface
}

func NewClients(base *rest.Config) *Clients {
	return &Clients{base: base, clients: map[string]kubernetes.Interface{}}
}

func (c *Clients) For(u User) (kubernetes.Interface, error) {
	k := u.key()
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, ok := c.clients[k]; ok {
		return cl, nil
	}
	cl, err := kubernetes.NewForConfig(c.configFor(u))
	if err != nil {
		return nil, err
	}
	// Borne simple : au-delà de 1 000 identités, on repart de zéro.
	if len(c.clients) >= 1000 {
		c.clients = map[string]kubernetes.Interface{}
	}
	c.clients[k] = cl
	return cl, nil
}

func (c *Clients) configFor(u User) *rest.Config {
	cfg := rest.CopyConfig(c.base)
	cfg.Impersonate = rest.ImpersonationConfig{UserName: u.Name, Groups: u.Groups}
	return cfg
}
