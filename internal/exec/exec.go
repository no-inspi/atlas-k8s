// Package exec ouvre un terminal interactif dans un container, au nom de
// l'utilisateur (équivalent de kubectl exec -it). Le navigateur envoie stdin en
// messages binaires et les redimensionnements en JSON ; le serveur renvoie la
// sortie du terminal en messages binaires.
package exec

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/no-inspi/atlas-k8s/internal/access"
)

// Size est la taille du terminal du navigateur.
type Size struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Resize <-chan Size
}

// Backend lance command dans le container avec un TTY et relie les flux
// jusqu'à la fin de la session. Une fin normale renvoie nil ; un code de sortie
// non nul est une *ExitError.
type Backend interface {
	Exec(ctx context.Context, u access.User, namespace, pod, container string, command []string, s Streams) error
}

// ExitError : le shell s'est terminé avec un code non nul.
type ExitError struct{ Code int }

func (e *ExitError) Error() string {
	return "le shell s'est terminé avec le code " + strconv.Itoa(e.Code)
}

// Shells essayés dans l'ordre quand le navigateur ne précise rien (spec).
var Shells = []string{"/bin/bash", "/bin/sh"}

// ErrNoShell : aucun shell dans l'image (distroless).
var ErrNoShell = errors.New("aucun shell trouvé dans le container (/bin/bash, /bin/sh) : image distroless ?")

// MissingExecutable reconnaît l'erreur du runtime quand le binaire n'existe pas.
func MissingExecutable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no such file or directory") || strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "not found in $PATH")
}
