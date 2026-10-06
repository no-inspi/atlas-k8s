package kube

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/no-inspi/atlas-k8s/internal/access"
	"github.com/no-inspi/atlas-k8s/internal/exec"
)

// Exec ouvre pods/exec au nom de l'utilisateur : protocole WebSocket
// v5.channel.k8s.io, repli sur SPDY si l'API server ne le propose pas.
type Exec struct{ clients access.ClientSource }

func NewExec(clients access.ClientSource) *Exec { return &Exec{clients: clients} }

var _ exec.Backend = (*Exec)(nil)

func (e *Exec) Exec(ctx context.Context, u access.User, ns, pod, container string, command []string, s exec.Streams) error {
	kc, err := e.clients.Kube(u)
	if err != nil {
		return err
	}
	cfg, err := e.clients.Config(u)
	if err != nil {
		return err
	}
	req := kc.CoreV1().RESTClient().Post().Resource("pods").Namespace(ns).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{Container: container, Command: command, Stdin: true, Stdout: true, TTY: true}, scheme.ParameterCodec)

	ws, err := remotecommand.NewWebSocketExecutor(cfg, "GET", req.URL().String())
	if err != nil {
		return err
	}
	spdy, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return err
	}
	exe, err := remotecommand.NewFallbackExecutor(ws, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	if err != nil {
		return err
	}
	err = exe.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin: s.Stdin, Stdout: s.Stdout, Tty: true, TerminalSizeQueue: sizeQueue(s.Resize),
	})
	var code utilexec.CodeExitError
	if errors.As(err, &code) {
		return &exec.ExitError{Code: code.Code}
	}
	return err
}

// sizeQueue adapte le canal de redimensionnement à remotecommand.
type sizeQueue <-chan exec.Size

func (q sizeQueue) Next() *remotecommand.TerminalSize {
	sz, ok := <-q
	if !ok {
		return nil
	}
	return &remotecommand.TerminalSize{Width: sz.Cols, Height: sz.Rows}
}
