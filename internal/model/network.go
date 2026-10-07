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
	BackendIndirect = "indirect" // TraefikService, non résolu
)

type Backend struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Port      string `json:"port,omitempty"`
	Kind      string `json:"kind"` // Service | TraefikService
	State     string `json:"state"`
}

type Rule struct {
	Host    string  `json:"host,omitempty"`
	Path    string  `json:"path,omitempty"`
	Match   string  `json:"match,omitempty"` // règle Traefik brute
	Backend Backend `json:"backend"`
}

// Route : une Ingress ou une IngressRoute Traefik, rattachée à sa porte
// (le contrôleur d'entrée qui la sert).
type Route struct {
	Source    string   `json:"source"` // Ingress | IngressRoute
	Group     string   `json:"group"`  // networking.k8s.io | traefik.io | traefik.containo.us
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Gate      string   `json:"gate"`
	Rules     []Rule   `json:"rules"`
	Addresses []string `json:"addresses,omitempty"`
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

func ServiceKey(s Service) string { return s.Namespace + "/" + s.Name }
func RouteKey(r Route) string     { return r.Source + "/" + r.Namespace + "/" + r.Name }
func VolumeKey(v Volume) string   { return v.Namespace + "/" + v.Name }
