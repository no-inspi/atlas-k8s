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
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/no-inspi/atlas-k8s/internal/access"
	"github.com/no-inspi/atlas-k8s/internal/audit"
	"github.com/no-inspi/atlas-k8s/internal/auth"
	"github.com/no-inspi/atlas-k8s/internal/demo"
	"github.com/no-inspi/atlas-k8s/internal/kube"
	"github.com/no-inspi/atlas-k8s/internal/server"
	"github.com/no-inspi/atlas-k8s/internal/stream"
	"github.com/no-inspi/atlas-k8s/web"
)

// version est injectée au build (-ldflags "-X main.version=…").
var version = "dev"

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
	addr, metricsAddr, clusterName, authMode, kubeconfig, kubeContext, poolLabel string
	demo                                                                         bool
	oidc                                                                         oidcFlags
	features                                                                     server.Features
	demoScale                                                                    demo.Scale
}

type oidcFlags struct {
	issuer, clientID, clientSecret, cookieKey, publicURL string
	usernameClaim, groupsClaim, groupsPrefix, scopes     string
	sessionTTL                                           time.Duration
}

func parseFlags() flags {
	var f flags
	flag.StringVar(&f.addr, "addr", env("ATLAS_ADDR", ":8080"), "adresse d'écoute HTTP")
	flag.StringVar(&f.metricsAddr, "metrics-addr", env("ATLAS_METRICS_ADDR", ":9090"), "adresse des métriques Prometheus (vide : désactivé)")
	flag.Func("demo-scale", "cluster simulé agrandi, NODESxPODS_PAR_NODE (ex. 100x30) : banc de performance", func(v string) error {
		_, err := fmt.Sscanf(v, "%dx%d", &f.demoScale.Nodes, &f.demoScale.PodsPerNode)
		if err != nil || f.demoScale.Nodes < 1 || f.demoScale.PodsPerNode < 1 {
			return fmt.Errorf("format attendu NODESxPODS_PAR_NODE, par ex. 100x30")
		}
		return nil
	})
	flag.BoolVar(&f.demo, "demo", env("ATLAS_DEMO", "") == "true", "sert un cluster simulé, sans API server")
	flag.StringVar(&f.clusterName, "cluster-name", env("ATLAS_CLUSTER_NAME", "gke-prod-europe-west1"), "nom du cluster affiché")
	flag.StringVar(&f.authMode, "auth-mode", env("ATLAS_AUTH_MODE", "oidc"), "oidc | none (développement uniquement)")
	flag.StringVar(&f.kubeconfig, "kubeconfig", env("KUBECONFIG", ""), "kubeconfig hors cluster (vide : in-cluster ou ~/.kube/config)")
	flag.StringVar(&f.kubeContext, "context", env("ATLAS_CONTEXT", ""), "contexte du kubeconfig")
	flag.StringVar(&f.poolLabel, "pool-label", env("ATLAS_POOL_LABEL", ""), "label désignant le node pool (vide : détection automatique)")
	o := &f.oidc
	flag.StringVar(&o.issuer, "oidc-issuer-url", env("ATLAS_OIDC_ISSUER_URL", ""), "issuer OIDC")
	flag.StringVar(&o.clientID, "oidc-client-id", env("ATLAS_OIDC_CLIENT_ID", ""), "client ID OIDC")
	flag.StringVar(&o.clientSecret, "oidc-client-secret", env("ATLAS_OIDC_CLIENT_SECRET", ""), "client secret OIDC (préférer la variable d'environnement)")
	flag.StringVar(&o.cookieKey, "cookie-key", env("ATLAS_COOKIE_KEY", ""), "clé de chiffrement des cookies : base64 de 32 octets (préférer la variable d'environnement)")
	flag.StringVar(&o.publicURL, "public-url", env("ATLAS_PUBLIC_URL", ""), "URL de l'application vue du navigateur (URL de retour OIDC)")
	flag.StringVar(&o.usernameClaim, "oidc-username-claim", env("ATLAS_OIDC_USERNAME_CLAIM", "email"), "claim du nom d'utilisateur")
	flag.StringVar(&o.groupsClaim, "oidc-groups-claim", env("ATLAS_OIDC_GROUPS_CLAIM", "groups"), "claim des groupes")
	flag.StringVar(&o.groupsPrefix, "oidc-groups-prefix", env("ATLAS_OIDC_GROUPS_PREFIX", "oidc:"), "préfixe des groupes impersonnés")
	flag.StringVar(&o.scopes, "oidc-scopes", env("ATLAS_OIDC_SCOPES", "openid,email,profile"), "scopes OIDC, séparés par des virgules")
	ttl, _ := time.ParseDuration(env("ATLAS_SESSION_TTL", "8h"))
	flag.DurationVar(&o.sessionTTL, "session-ttl", ttl, "durée de vie d'une session")
	ft := &f.features
	flag.BoolVar(&ft.ExecEnabled, "exec", env("ATLAS_EXEC_ENABLED", "true") == "true", "terminal dans les containers")
	denied := flag.String("exec-denied-namespaces", env("ATLAS_EXEC_DENIED_NAMESPACES", "kube-system"), "namespaces interdits au terminal, séparés par des virgules")
	idle, _ := time.ParseDuration(env("ATLAS_EXEC_IDLE_TIMEOUT", "15m"))
	flag.DurationVar(&ft.ExecIdleTimeout, "exec-idle-timeout", idle, "fermeture d'un terminal inactif")
	flag.BoolVar(&ft.ActionsEnabled, "actions", env("ATLAS_ACTIONS_ENABLED", "true") == "true", "actions d'exploitation (false : console en lecture seule)")
	flag.Parse()
	ft.ExecDeniedNamespaces = splitList(*denied)
	if f.demoScale.Nodes > 0 {
		f.demo = true // --demo-scale implique --demo
	}
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
	cfg := server.Config{ClusterName: f.clusterName, Demo: f.demo, Static: static,
		Audit: audit.New(os.Stdout), Features: f.features}

	if f.demo {
		cfg.User = "demo"
		sim := demo.New(hub, demo.Options{Seed: uint64(time.Now().UnixNano()), Scale: f.demoScale})
		hub.MarkReady()
		cfg.Inspect, cfg.Actions, cfg.Exec = sim, sim, sim
		go sim.Run(ctx)
	} else {
		rc, err := kube.RestConfig(f.kubeconfig, f.kubeContext)
		if err != nil {
			return fmt.Errorf("connexion à l'API server : %w", err)
		}
		client, err := kubernetes.NewForConfig(rc)
		if err != nil {
			return err
		}
		switch f.authMode {
		case "none":
			log.Warn("authentification désactivée (--auth-mode=none) : réservé au développement")
			cfg.User, cfg.Kube = "anonymous", client
		case "oidc":
			if cfg.Auth, err = newAuth(ctx, f.oidc, log); err != nil {
				return err
			}
			cfg.Reviewer = access.NewReviewer(client)
			cfg.Clients = access.NewClients(rc)
		default:
			return fmt.Errorf("--auth-mode inconnu %q (oidc | none)", f.authMode)
		}
		src, err := startClusterSource(ctx, f, rc, client, hub, log)
		if err != nil {
			return err
		}
		var clients access.ClientSource = cfg.Clients
		var authorize kube.Authorize
		if cfg.Auth != nil {
			authorize = cfg.Reviewer.Allowed
		} else {
			dyn, err := dynamic.NewForConfig(rc)
			if err != nil {
				return err
			}
			clients = access.Static{K: client, D: dyn, C: rc}
		}
		cfg.Inspect = kube.NewInspector(clients, src, authorize)
		// ATLAS_POD_* : downward API du chart, pour qu'un drain n'évince pas Atlas en pleine requête.
		cfg.Actions = kube.NewActions(clients).WithSelf(os.Getenv("ATLAS_POD_NAMESPACE"), os.Getenv("ATLAS_POD_NAME"))
		cfg.Exec = kube.NewExec(clients)
	}
	go hub.Run(ctx)

	srv := &http.Server{Addr: f.addr, Handler: server.New(cfg, hub, log), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 2)
	go func() { errc <- srv.ListenAndServe() }()
	var metrics *http.Server
	if f.metricsAddr != "" {
		metrics = &http.Server{Addr: f.metricsAddr, Handler: server.MetricsHandler(hub), ReadHeaderTimeout: 10 * time.Second}
		go func() { errc <- metrics.ListenAndServe() }()
	}
	log.Info("atlas démarré", "version", version, "addr", f.addr, "demo", f.demo, "cluster", f.clusterName)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("arrêt en cours")
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if metrics != nil {
		_ = metrics.Shutdown(shutdown)
	}
	return srv.Shutdown(shutdown)
}

// splitList découpe une liste séparée par des virgules, sans éléments vides.
func splitList(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func newAuth(ctx context.Context, o oidcFlags, log *slog.Logger) (*auth.Auth, error) {
	if o.issuer == "" || o.clientID == "" {
		return nil, errors.New("auth-mode=oidc : --oidc-issuer-url et --oidc-client-id sont requis (ou --auth-mode=none en développement)")
	}
	key, err := auth.ParseKey(o.cookieKey)
	if err != nil {
		return nil, fmt.Errorf("%w (ATLAS_COOKIE_KEY ; générer avec : openssl rand -base64 32)", err)
	}
	scopes := splitList(o.scopes)
	return auth.New(ctx, auth.Config{
		IssuerURL: o.issuer, ClientID: o.clientID, ClientSecret: o.clientSecret, PublicURL: o.publicURL,
		Scopes: scopes, UsernameClaim: o.usernameClaim, GroupsClaim: o.groupsClaim, GroupsPrefix: o.groupsPrefix,
		SessionTTL: o.sessionTTL, CookieKey: key,
	}, log)
}

// startClusterSource branche les informers et metrics-server sur le hub.
func startClusterSource(ctx context.Context, f flags, rc *rest.Config, client kubernetes.Interface, hub *stream.Hub, log *slog.Logger) (*kube.Source, error) {
	mc, err := metricsclient.NewForConfig(rc)
	if err != nil {
		return nil, err
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
	return src, nil
}
