package demo

// Catalogue du cluster simulé, repris du prototype (docs/prototype.html) et
// complété pour couvrir tous les types de workloads et de statuts de la spec.

const (
	mi = int64(1) << 20
	gi = int64(1) << 30
)

type poolDef struct {
	Name, Machine string
	CPU           int64 // allocatable, millicores
	Mem           int64 // allocatable, octets
	CapCPU        int64 // capacité nominale, millicores
	CapMem        int64 // capacité nominale, octets
	Spot          bool
	GPU           int
	Count         int
}

var pools = []poolDef{
	{Name: "default-pool", Machine: "e2-standard-4", CPU: 3920, Mem: 13000 * mi, CapCPU: 4000, CapMem: 16 * gi, Count: 3},
	{Name: "spot-pool", Machine: "e2-standard-8", CPU: 7910, Mem: 27000 * mi, CapCPU: 8000, CapMem: 32 * gi, Spot: true, Count: 2},
	{Name: "gpu-pool", Machine: "g2-standard-8", CPU: 7910, Mem: 27000 * mi, CapCPU: 8000, CapMem: 32 * gi, GPU: 1, Count: 1},
}

var zones = []string{"europe-west1-b", "europe-west1-c", "europe-west1-d"}

const (
	kubeletVersion = "v1.31.4-gke.1256000"
	registry       = "europe-west1-docker.pkg.dev/prod/apps/"
)

type workloadDef struct {
	Kind      string // Deployment | StatefulSet | DaemonSet | Job
	Name, NS  string
	Image     string
	Replicas  int32
	CPU, Mem  int64
	Crashy    bool // un replica boucle en CrashLoopBackOff
	GPU       bool // demande nvidia.com/gpu et tolère le taint
	NotReady  bool // un replica reste Running non ready
	BadImage  bool // ImagePullBackOff
	Argo      bool
	ToleraAll bool // DaemonSet : tolère tous les taints
}

var workloads = []workloadDef{
	{Kind: "Deployment", Name: "api-gateway", NS: "production", Replicas: 3, Image: registry + "api-gateway:2.14.1", CPU: 250, Mem: 256 * mi, Argo: true},
	{Kind: "Deployment", Name: "orders-service", NS: "production", Replicas: 3, Image: registry + "orders-service:1.9.0", CPU: 500, Mem: 512 * mi, Argo: true},
	{Kind: "Deployment", Name: "payment-worker", NS: "production", Replicas: 2, Image: registry + "payment-worker:3.2.0", CPU: 300, Mem: 384 * mi, Crashy: true, Argo: true},
	{Kind: "Deployment", Name: "frontend", NS: "production", Replicas: 2, Image: registry + "frontend:5.0.3", CPU: 100, Mem: 128 * mi, Argo: true},
	{Kind: "Deployment", Name: "ml-inference", NS: "production", Replicas: 1, Image: registry + "ml-inference:0.8.2", CPU: 2000, Mem: 8 * gi, GPU: true, Argo: true},
	{Kind: "StatefulSet", Name: "postgres-payments", NS: "production", Replicas: 2, Image: "docker.io/library/postgres:16.4", CPU: 500, Mem: 1 * gi, Argo: true},
	{Kind: "Job", Name: "db-backup", NS: "production", Replicas: 1, Image: registry + "db-backup:1.2.0", CPU: 200, Mem: 256 * mi},
	{Kind: "Deployment", Name: "api-gateway", NS: "staging", Replicas: 1, Image: registry + "api-gateway:2.15.0-rc1", CPU: 250, Mem: 256 * mi, Argo: true},
	{Kind: "Deployment", Name: "orders-service", NS: "staging", Replicas: 2, Image: registry + "orders-service:1.10.0-rc2", CPU: 500, Mem: 512 * mi, NotReady: true, Argo: true},
	{Kind: "Deployment", Name: "checkout-preview", NS: "staging", Replicas: 1, Image: registry + "checkout-preview:does-not-exist", CPU: 100, Mem: 128 * mi, BadImage: true, Argo: true},
	{Kind: "Deployment", Name: "prometheus", NS: "monitoring", Replicas: 1, Image: "quay.io/prometheus/prometheus:v2.54.1", CPU: 1000, Mem: 2 * gi, Argo: true},
	{Kind: "Deployment", Name: "grafana", NS: "monitoring", Replicas: 1, Image: "docker.io/grafana/grafana:11.2.0", CPU: 200, Mem: 256 * mi, Argo: true},
	{Kind: "DaemonSet", Name: "node-exporter", NS: "monitoring", Image: "quay.io/prometheus/node-exporter:v1.8.2", CPU: 50, Mem: 64 * mi, ToleraAll: true, Argo: true},
	{Kind: "Deployment", Name: "argocd-server", NS: "argocd", Replicas: 1, Image: "quay.io/argoproj/argocd:v2.12.3", CPU: 250, Mem: 256 * mi},
	{Kind: "Deployment", Name: "argocd-repo-server", NS: "argocd", Replicas: 1, Image: "quay.io/argoproj/argocd:v2.12.3", CPU: 250, Mem: 512 * mi},
	{Kind: "Deployment", Name: "coredns", NS: "kube-system", Replicas: 2, Image: "registry.k8s.io/coredns/coredns:v1.11.3", CPU: 100, Mem: 70 * mi},
	{Kind: "Deployment", Name: "metrics-server", NS: "kube-system", Replicas: 1, Image: "registry.k8s.io/metrics-server/metrics-server:v0.7.2", CPU: 100, Mem: 200 * mi},
}

// Durées du cycle de vie, reprises du prototype.
const (
	pendingDelaySec      = 0.9  // un pod attend avant d'être placé
	creatingMinSec       = 1.0  // ContainerCreating
	creatingMaxSec       = 1.8  //
	firstCrashMinSec     = 2.5  // premier crash après démarrage
	firstCrashMaxSec     = 4.5  //
	nextCrashMinSec      = 6.0  // crashs suivants
	nextCrashMaxSec      = 16.0 //
	errorSec             = 1.2  // Error avant CrashLoopBackOff
	backoffSec           = 5.8  // CrashLoopBackOff avant redémarrage
	terminatingSec       = 0.9  // Terminating avant disparition
	jobPeriodSec         = 45.0 // un Job db-backup toutes les 45 s
	jobFirstSec          = 5.0  //
	jobRunSec            = 8.0  // durée d'un Job
	jobTTLSec            = 30.0 // ttlSecondsAfterFinished
	churnPeriodSec       = 16.0 // rollout de staging
	churnProbability     = 0.6  //
	metricsPeriodSec     = 15.0 // metrics-server
	stepIntervalMillisec = 200
)
