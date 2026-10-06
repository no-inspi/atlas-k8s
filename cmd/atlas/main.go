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

	"github.com/no-inspi/cluster-atlas/internal/demo"
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

func run() error {
	addr := flag.String("addr", env("ATLAS_ADDR", ":8080"), "adresse d'écoute HTTP")
	demoMode := flag.Bool("demo", env("ATLAS_DEMO", "") == "true", "sert un cluster simulé, sans API server")
	clusterName := flag.String("cluster-name", env("ATLAS_CLUSTER_NAME", "gke-prod-europe-west1"), "nom du cluster affiché")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if !*demoMode {
		return errors.New("mode cluster disponible au jalon 2 ; lancez `atlas --demo`")
	}

	static, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hub := stream.NewHub(stream.Options{BaseRev: stream.TimeBaseRev()})
	sim := demo.New(hub, demo.Options{Seed: uint64(time.Now().UnixNano())})
	hub.MarkReady()
	go hub.Run(ctx)
	go sim.Run(ctx)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.New(server.Config{ClusterName: *clusterName, Demo: true, Static: static}, hub, log),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("atlas démarré", "addr", *addr, "demo", true, "cluster", *clusterName)

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
