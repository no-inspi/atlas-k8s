package demo

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/no-inspi/cluster-atlas/internal/exec"
)

// runShell joue une saisie clavier dans le shell simulé et renvoie la sortie.
func runShell(t *testing.T, s *Sim, pod, input string) (string, error) {
	t.Helper()
	inR, inW := io.Pipe()
	var out strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- s.Exec(context.Background(), anyone, "production", pod, "", []string{"/bin/sh"}, exec.Streams{Stdin: inR, Stdout: &out, Resize: make(chan exec.Size)})
	}()
	for _, b := range []byte(input) {
		_, _ = inW.Write([]byte{b})
	}
	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(2 * time.Second):
		inW.Close()
		return out.String(), <-done
	}
}

func TestDemoShell(t *testing.T) {
	s, sink := start(31)
	api := findPod(sink, "production", "api-gateway", "Running")

	out, err := runShell(t, s, api.Name, "hostname\recho hello-42\rhos\x7f\x7fst\x03ls -la\x1b[A\rnope\rexit\r")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/app $ ", api.Name, "hello-42", "^C", "/bin/sh: nope: not found"} {
		if !strings.Contains(out, want) {
			t.Errorf("sortie sans %q :\n%q", want, out)
		}
	}
	if strings.Contains(out, "[A") {
		t.Error("les séquences d'échappement (flèches) ne doivent pas s'afficher")
	}

	if _, err := runShell(t, s, api.Name, "exit 3\r"); err == nil {
		t.Error("exit 3 doit remonter le code de sortie")
	} else if e, ok := err.(*exec.ExitError); !ok || e.Code != 3 {
		t.Errorf("erreur = %v", err)
	}
}

func TestDemoShellRefusesBashAndStoppedPods(t *testing.T) {
	s, sink := start(32)
	api := findPod(sink, "production", "api-gateway", "Running")
	err := s.Exec(context.Background(), anyone, "production", api.Name, "", []string{"/bin/bash"}, exec.Streams{})
	if !exec.MissingExecutable(err) {
		t.Errorf("les images de démo n'ont pas bash : %v", err)
	}
	broken := findPod(sink, "staging", "checkout-preview", "")
	if err := s.Exec(context.Background(), anyone, "staging", broken.Name, "", []string{"/bin/sh"}, exec.Streams{}); err == nil ||
		!strings.Contains(err.Error(), "not running") {
		t.Errorf("container non démarré : %v", err)
	}
}
