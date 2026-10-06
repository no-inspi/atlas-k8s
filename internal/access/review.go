package access

import (
	"context"
	"fmt"

	authzv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// MaxChecks borne un lot de /api/access-review.
const MaxChecks = 50

type Check struct {
	Verb        string `json:"verb"`
	Group       string `json:"group,omitempty"`
	Resource    string `json:"resource"`
	Subresource string `json:"subresource,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Name        string `json:"name,omitempty"`
}

type Result struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

// Review pose une SelfSubjectAccessReview par vérification, avec le client
// impersonné de l'utilisateur : c'est l'API server qui répond pour lui.
func Review(ctx context.Context, client kubernetes.Interface, checks []Check) ([]Result, error) {
	if len(checks) > MaxChecks {
		return nil, fmt.Errorf("au plus %d vérifications par lot", MaxChecks)
	}
	out := make([]Result, len(checks))
	for i, c := range checks {
		s := &authzv1.SelfSubjectAccessReview{Spec: authzv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authzv1.ResourceAttributes{
				Verb: c.Verb, Group: c.Group, Resource: c.Resource, Subresource: c.Subresource, Namespace: c.Namespace, Name: c.Name,
			},
		}}
		res, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, s, metav1.CreateOptions{})
		if err != nil {
			out[i] = Result{Reason: "vérification impossible : " + err.Error()}
			continue
		}
		out[i] = Result{Allowed: res.Status.Allowed, Reason: res.Status.Reason}
	}
	return out, nil
}
