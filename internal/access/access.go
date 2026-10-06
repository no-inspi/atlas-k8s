// Package access applique le RBAC du cluster à l'utilisateur connecté :
// SubjectAccessReview pour filtrer le flux, clients impersonnés pour tout le
// reste, SelfSubjectAccessReview pour griser les actions.
package access

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// User est l'identité impersonnée (groupes déjà préfixés).
type User struct {
	Name   string
	Groups []string
}

func (u User) key() string { return u.Name + "\x00" + strings.Join(u.Groups, "\x00") }

// Attributes décrit une demande d'accès à une ressource.
type Attributes struct {
	Verb, Group, Resource, Subresource, Namespace, Name string
}

const (
	cacheTTL = 60 * time.Second
	errorTTL = 5 * time.Second
)

type decision struct {
	allowed bool
	expires time.Time
}

// Reviewer pose des SubjectAccessReview avec le ServiceAccount et garde les
// décisions 60 s par utilisateur et attributs (spec, « Cache partagé et filtrage »).
type Reviewer struct {
	client kubernetes.Interface
	log    *slog.Logger
	now    func() time.Time

	mu    sync.Mutex
	cache map[string]decision
}

func NewReviewer(client kubernetes.Interface) *Reviewer {
	return &Reviewer{client: client, log: slog.Default(), now: time.Now, cache: map[string]decision{}}
}

// Allowed ne renvoie vrai que sur une autorisation explicite de l'API server :
// une erreur vaut refus.
func (r *Reviewer) Allowed(ctx context.Context, u User, a Attributes) bool {
	k := u.key() + "\x01" + a.Verb + "/" + a.Group + "/" + a.Resource + "/" + a.Subresource + "/" + a.Namespace + "/" + a.Name
	now := r.now()
	r.mu.Lock()
	d, ok := r.cache[k]
	r.mu.Unlock()
	if ok && now.Before(d.expires) {
		return d.allowed
	}

	sar := &authzv1.SubjectAccessReview{Spec: authzv1.SubjectAccessReviewSpec{
		User: u.Name,
		// L'impersonation ajoute system:authenticated ; une SAR ne le fait pas.
		Groups: append(append([]string{}, u.Groups...), "system:authenticated"),
		ResourceAttributes: &authzv1.ResourceAttributes{
			Verb: a.Verb, Group: a.Group, Resource: a.Resource, Subresource: a.Subresource, Namespace: a.Namespace, Name: a.Name,
		},
	}}
	res, err := r.client.AuthorizationV1().SubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
	d = decision{expires: now.Add(cacheTTL)}
	if err != nil {
		r.log.Warn("SubjectAccessReview impossible, accès refusé", "user", u.Name, "resource", a.Resource, "namespace", a.Namespace, "err", err)
		d.expires = now.Add(errorTTL)
	} else {
		d.allowed = res.Status.Allowed
	}
	r.mu.Lock()
	r.cache[k] = d
	if len(r.cache) > 50_000 {
		r.pruneLocked(now)
	}
	r.mu.Unlock()
	return d.allowed
}

func (r *Reviewer) pruneLocked(now time.Time) {
	for k, d := range r.cache {
		if now.After(d.expires) {
			delete(r.cache, k)
		}
	}
}
