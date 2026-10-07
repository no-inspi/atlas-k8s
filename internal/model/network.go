package model

// Santé d'un Service : couleur du voyant de son relais.
const (
	HealthOK       = "ok"
	HealthDegraded = "degraded"
	HealthDown     = "down"
	HealthExternal = "external"
)

type ServicePort struct {
	Name       string `json:"name,omitempty"`
	Port       int32  `json:"port"`
	TargetPort string `json:"targetPort,omitempty"`
	Protocol   string `json:"protocol"`
	NodePort   int32  `json:"nodePort,omitempty"`
}

// Endpoint : un pod derrière un Service, lu dans ses EndpointSlices.
type Endpoint struct {
	PodUID string `json:"podUID"`
	Ready  bool   `json:"ready"`
}

type Service struct {
	Namespace    string        `json:"namespace"`
	Name         string        `json:"name"`
	Type         string        `json:"type"` // ClusterIP | NodePort | LoadBalancer | ExternalName
	Headless     bool          `json:"headless,omitempty"`
	ClusterIP    string        `json:"clusterIP,omitempty"`
	Ports        []ServicePort `json:"ports"`
	LoadBalancer []string      `json:"loadBalancer,omitempty"`
	ExternalName string        `json:"externalName,omitempty"`
	Endpoints    []Endpoint    `json:"endpoints"`
	Health       string        `json:"health"`
}

// ServiceHealth : ok si toutes les adresses sont ready, degraded si une partie,
// down si aucune (ou aucune adresse) ; external pour un ExternalName.
func ServiceHealth(typ string, total, ready int) string {
	switch {
	case typ == "ExternalName":
		return HealthExternal
	case ready == 0:
		return HealthDown
	case ready < total:
		return HealthDegraded
	}
	return HealthOK
}

// État du backend d'une règle de route.
const (
	BackendOK       = "ok"
	BackendMissing  = "missing"  // Service introuvable
	BackendRefused  = "refused"  // route refusée par toutes ses Gateways (Accepted=False)
	BackendIndirect = "indirect" // TraefikService non résolu (jalon 8 ; plus produit, gardé pour la compatibilité)
)

// Sources d'une route.
const (
	SourceIngress         = "Ingress"
	SourceIngressRoute    = "IngressRoute"
	SourceIngressRouteTCP = "IngressRouteTCP"
	SourceIngressRouteUDP = "IngressRouteUDP"
	SourceHTTPRoute       = "HTTPRoute"
	SourceGRPCRoute       = "GRPCRoute"
)

// Tri-état d'une condition Kubernetes ; unknown : condition absente (pas de contrôleur).
const (
	CondTrue    = "true"
	CondFalse   = "false"
	CondUnknown = "unknown"
)

// NoGateway : porte d'une route Gateway API sans parentRef de kind Gateway.
const NoGateway = "(sans gateway)"

// Weight : poids d'un backend (un poids nul doit rester publié, d'où le pointeur).
func Weight(n int) *int { return &n }

type Backend struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Port      string `json:"port,omitempty"`
	Kind      string `json:"kind"` // Service | TraefikService
	State     string `json:"state"`
	Weight    *int   `json:"weight,omitempty"`  // part du trafic de la règle source, pour mille ; nil : règle à un seul backend
	Mirror    bool   `json:"mirror,omitempty"`  // copie du trafic (miroir Traefik)
	Percent   int    `json:"percent,omitempty"` // part du trafic copiée vers le miroir
	Via       string `json:"via,omitempty"`     // « ns/name » du TraefikService racine
}

type Rule struct {
	Host    string  `json:"host,omitempty"`
	Path    string  `json:"path,omitempty"`
	Match   string  `json:"match,omitempty"` // règle Traefik brute
	Backend Backend `json:"backend"`
}

// RouteParent : état d'une route Gateway API vis-à-vis d'un de ses Gateways.
type RouteParent struct {
	Gateway      string `json:"gateway"` // « ns/name »
	Accepted     string `json:"accepted"`
	ResolvedRefs string `json:"resolvedRefs"`
	Reason       string `json:"reason,omitempty"`
}

// Route : une Ingress, une IngressRoute Traefik (HTTP, TCP, UDP) ou une route
// Gateway API, rattachée à ses portes (contrôleurs d'entrée ou Gateways).
type Route struct {
	Source    string        `json:"source"` // une des constantes Source*
	Group     string        `json:"group"`  // networking.k8s.io | traefik.io | traefik.containo.us | gateway.networking.k8s.io
	Namespace string        `json:"namespace"`
	Name      string        `json:"name"`
	Gate      string        `json:"gate"`  // Gates[0], pour la compatibilité
	Gates     []string      `json:"gates"` // jamais vide
	Rules     []Rule        `json:"rules"`
	Addresses []string      `json:"addresses,omitempty"`
	Parents   []RouteParent `json:"parents,omitempty"`
}

// Volume : un PersistentVolumeClaim et les pods qui le montent. Tailles en octets.
type Volume struct {
	Namespace    string   `json:"namespace"`
	Name         string   `json:"name"`
	StorageClass string   `json:"storageClass"`
	Requested    int64    `json:"requested"`
	Capacity     int64    `json:"capacity"`
	AccessModes  []string `json:"accessModes"`
	Phase        string   `json:"phase"` // Pending | Bound | Lost
	VolumeName   string   `json:"volumeName,omitempty"`
	Pods         []string `json:"pods"`
}

type Listener struct {
	Name           string `json:"name"`
	Protocol       string `json:"protocol"`
	Port           int32  `json:"port"`
	Hostname       string `json:"hostname,omitempty"`
	AttachedRoutes int32  `json:"attachedRoutes"`
	Ready          string `json:"ready"`
}

// Gateway : un Gateway de la Gateway API, dessiné en porte.
type Gateway struct {
	Namespace  string     `json:"namespace"`
	Name       string     `json:"name"`
	Class      string     `json:"class"`
	Accepted   string     `json:"accepted"`
	Programmed string     `json:"programmed"`
	Reason     string     `json:"reason,omitempty"`
	Message    string     `json:"message,omitempty"`
	Addresses  []string   `json:"addresses,omitempty"`
	Listeners  []Listener `json:"listeners"`
}

// PersistentVolume : un PV sans PVC existant (citerne vide). Capacité en octets.
type PersistentVolume struct {
	Name          string   `json:"name"`
	StorageClass  string   `json:"storageClass"`
	Capacity      int64    `json:"capacity"`
	AccessModes   []string `json:"accessModes"`
	ReclaimPolicy string   `json:"reclaimPolicy"`
	Phase         string   `json:"phase"`              // Available | Released | Failed | Bound
	ClaimRef      string   `json:"claimRef,omitempty"` // « ns/name »
}

func ServiceKey(s Service) string                   { return s.Namespace + "/" + s.Name }
func RouteKey(r Route) string                       { return r.Source + "/" + r.Namespace + "/" + r.Name }
func VolumeKey(v Volume) string                     { return v.Namespace + "/" + v.Name }
func GatewayKey(g Gateway) string                   { return g.Namespace + "/" + g.Name }
func PersistentVolumeKey(p PersistentVolume) string { return p.Name }
