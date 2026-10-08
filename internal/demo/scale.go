package demo

import (
	"fmt"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// Scale décrit un cluster simulé agrandi pour mesurer les performances
// (atlas --demo --demo-scale=100x30) : Nodes nodes et environ PodsPerNode pods
// par node.
type Scale struct {
	Nodes       int
	PodsPerNode int
}

type catalog struct {
	pools     []poolDef
	workloads []workloadDef
	services  []serviceDef
	routes    []model.Route
	volumes   []volumeDef
	gateways  []model.Gateway
	pvs       []model.PersistentVolume
}

// Workloads d'une équipe simulée : 25 pods, CPU modeste.
var teamWorkloads = []workloadDef{
	{Kind: "Deployment", Name: "api", Replicas: 6, CPU: 100, Mem: 128 * mi},
	{Kind: "Deployment", Name: "worker", Replicas: 6, CPU: 100, Mem: 192 * mi},
	{Kind: "Deployment", Name: "web", Replicas: 4, CPU: 50, Mem: 64 * mi},
	{Kind: "Deployment", Name: "cron-runner", Replicas: 2, CPU: 50, Mem: 64 * mi},
	{Kind: "StatefulSet", Name: "cache", Replicas: 3, CPU: 100, Mem: 256 * mi},
	{Kind: "StatefulSet", Name: "db", Replicas: 2, CPU: 200, Mem: 512 * mi},
	{Kind: "Deployment", Name: "gateway", Replicas: 2, CPU: 100, Mem: 128 * mi},
}

const teamPods = 25

func catalogFor(sc Scale) catalog {
	if sc.Nodes == 0 {
		return catalog{pools: pools, workloads: workloads, services: services, routes: baseRoutes, volumes: volumes,
			gateways: withAttachedRoutes(gateways, baseRoutes), pvs: orphanPVs}
	}
	// Répartition des nodes : 60 % standard, 30 % spot, le reste en GPU (au moins 1).
	gpu := max(1, sc.Nodes/25)
	spot := sc.Nodes * 3 / 10
	std := sc.Nodes - gpu - spot
	c := catalog{pools: []poolDef{
		{Name: "default-pool", Machine: "e2-standard-8", CPU: 7910, Mem: 27000 * mi, CapCPU: 8000, CapMem: 32 * gi, Count: std},
		{Name: "spot-pool", Machine: "e2-standard-8", CPU: 7910, Mem: 27000 * mi, CapCPU: 8000, CapMem: 32 * gi, Spot: true, Count: spot},
		{Name: "gpu-pool", Machine: "g2-standard-8", CPU: 7910, Mem: 27000 * mi, CapCPU: 8000, CapMem: 32 * gi, GPU: 1, Count: gpu},
	}}
	c.workloads = append(c.workloads, workloads...)
	c.services = append(c.services, services...)
	c.routes = append(c.routes, baseRoutes...)
	c.volumes = append(c.volumes, volumes...)
	c.gateways = append(c.gateways, gateways...)
	c.pvs = append(c.pvs, orphanPVs...)
	// Pods déjà prévus : catalogue de base (≈ 30) et un node-exporter par node.
	target := sc.Nodes*sc.PodsPerNode - 30 - sc.Nodes
	for i := 1; i <= max(0, target/teamPods); i++ {
		ns := fmt.Sprintf("team-%03d", i)
		for _, w := range teamWorkloads {
			w.NS, w.Image, w.Argo = ns, registry+w.Name+":1.0."+fmt.Sprint(i%10), i%3 != 0
			c.workloads = append(c.workloads, w)
		}
		svcs, routes, vols := teamNetwork(i, ns)
		c.services = append(c.services, svcs...)
		c.routes = append(c.routes, routes...)
		c.volumes = append(c.volumes, vols...)
		c.gateways = append(c.gateways, teamGateways(i, ns)...)
		c.pvs = append(c.pvs, teamPVs(i, ns)...)
	}
	c.gateways = withAttachedRoutes(c.gateways, c.routes)
	return c
}
