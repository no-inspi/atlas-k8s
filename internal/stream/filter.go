package stream

import "context"

// Filter décide ce qu'un client a le droit de recevoir. Il est appelé depuis la
// goroutine de ce client, jamais sous le verrou du hub : une décision lente
// (SubjectAccessReview) ne retarde que ce client.
type Filter interface {
	Allow(ctx context.Context, kind Kind, obj any) bool
	// Changed indique si l'une des décisions déjà prises a changé (droits
	// modifiés) : le client doit alors repartir d'un snapshot complet.
	Changed(ctx context.Context) bool
}

// AllowAll : modes démo et auth none.
type AllowAll struct{}

func (AllowAll) Allow(context.Context, Kind, any) bool { return true }
func (AllowAll) Changed(context.Context) bool          { return false }
