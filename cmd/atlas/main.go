// Commande atlas : console Kubernetes Cluster Atlas.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/no-inspi/cluster-atlas/internal/demo"
	"github.com/no-inspi/cluster-atlas/internal/kube"
	"github.com/no-inspi/cluster-atlas/internal/server"
	"github.com/no-inspi/cluster-atlas/internal/stream"
	"github.com/no-inspi/cluster-atlas/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "atlas:", err)
		os.Exit(2)
	}
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

type flags struct {
	addr, clusterName, authMode, kubeconfig, kubeContext, poolLabel string
	demo                                                            bool
}

func parseFlags() flags {
	var f flags
	flag.StringVar(&f.addr, "addr", env("ATLAS_ADDR", ":8080"), "adresse d'écoute HTTP")
	flag.BoolVar(&f.demo, "demo", env("ATLAS_DEMO", "") == "true", "sert un cluster simulé, sans API server")
	flag.StringVar(&f.clusterName, "cluster-name", env("ATLAS_CLUSTER_NAME", "gke-prod-europe-west1"), "nom du cluster affiché")
	flag.StringVar(&f.authMode, "auth-mode", env("ATLAS_AUTH_MODE", "oidc"), "oidc | none (développement uniquement)")
	flag.StringVar(&f.kubeconfig, "kubeconfig", env("KUBECONFIG", ""), "kubeconfig hors cluster (vide : in-cluster ou ~/.kube/config)")
	flag.StringVar(&f.kubeContext, "context", env("ATLAS_CONTEXT", ""), "contexte du kubeconfig")
	flag.StringVar(&f.poolLabel, "pool-label", env("ATLAS_POOL_LABEL", ""), "label désignant le node pool (vide : détection automatique)")
	flag.Parse()
	return f
}

func run() error {
	f := parseFlags()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	static, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hub := stream.NewHub(stream.Options{BaseRev: stream.TimeBaseRev()})
	cfg := server.Config{ClusterName: f.clusterName, Demo: f.demo, Static: static}

	if f.demo {
		cfg.User = "demo"
		sim := demo.New(hub, demo.Options{Seed: uint64(time.Now().UnixNano())})
		hub.MarkReady()
		go sim.Run(ctx)
	} else {
		if f.authMode != "none" {
			return errors.New("authentification OIDC disponible au jalon 4 ; en développement, lancez avec --auth-mode=none")
		}
		log.Warn("authentification désactivée (--auth-mode=none) : réservé au développement")
		cfg.User = "anonymous"
		if err := startClusterSource(ctx, f, hub, log); err != nil {
			return err
		}
	}
	go hub.Run(ctx)

	srv := &http.Server{Addr: f.addr, Handler: server.New(cfg, hub, log), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("atlas démarré", "addr", f.addr, "demo", f.demo, "cluster", f.clusterName)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("arrêt en cours")
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

// startClusterSource branche les informers et metrics-server sur le hub.
func startClusterSource(ctx context.Context, f flags, hub *stream.Hub, log *slog.Logger) error {
	rc, err := kube.RestConfig(f.kubeconfig, f.kubeContext)
	if err != nil {
		return fmt.Errorf("connexion à l'API server : %w", err)
	}
	client, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return err
	}
	mc, err := metricsclient.NewForConfig(rc)
	if err != nil {
		return err
	}
	src := kube.NewSource(client, hub, kube.Options{Node: kube.NodeOptions{PoolLabel: f.poolLabel}, Log: log})
	go func() {
		if err := src.Run(ctx); err != nil {
			log.Error("source Kubernetes arrêtée", "err", err)
		}
	}()
	go func() {
		// Les métriques ont besoin du cache des pods pour retrouver les UID.
		for !hub.Ready() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
		kube.NewMetricsPoller(mc.MetricsV1beta1(), hub, src.PodUID, log).Run(ctx)
	}()
	return nil
}
