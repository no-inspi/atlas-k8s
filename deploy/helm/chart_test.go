// Tests du chart : rendu par `helm template`, ignorés si helm est absent.
package helm

import (
	"bytes"
	"encoding/base64"
	"os/exec"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

type doc = map[string]any

func render(t *testing.T, args ...string) ([]doc, error) {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm absent")
	}
	cmd := exec.Command("helm", append([]string{"template", "atlas", "./cluster-atlas", "--namespace", "cluster-atlas"}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, &renderError{errb.String()}
	}
	var docs []doc
	for _, part := range strings.Split(out.String(), "\n---") {
		var d doc
		if err := yaml.Unmarshal([]byte(part), &d); err != nil {
			t.Fatalf("YAML invalide : %v\n%s", err, part)
		}
		if d != nil {
			docs = append(docs, d)
		}
	}
	return docs, nil
}

type renderError struct{ msg string }

func (e *renderError) Error() string { return e.msg }

func mustRender(t *testing.T, args ...string) []doc {
	t.Helper()
	docs, err := render(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	return docs
}

func find(docs []doc, kind string) doc {
	for _, d := range docs {
		if d["kind"] == kind {
			return d
		}
	}
	return nil
}

// get suit un chemin « a.b.0.c » dans un document.
func get(v any, path string) any {
	for _, k := range strings.Split(path, ".") {
		switch x := v.(type) {
		case map[string]any:
			v = x[k]
		case []any:
			i := 0
			for _, c := range k {
				i = i*10 + int(c-'0')
			}
			if i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}

var oidc = []string{"--set", "auth.oidc.clientID=atlas", "--set", "auth.oidc.clientSecret=s3cret", "--set", "publicURL=https://atlas.corp"}

func TestDefaultRender(t *testing.T) {
	docs := mustRender(t, oidc...)
	for _, k := range []string{"ServiceAccount", "ClusterRole", "ClusterRoleBinding", "Secret", "Deployment", "Service", "NetworkPolicy"} {
		if find(docs, k) == nil {
			t.Errorf("%s absent du rendu par défaut", k)
		}
	}
	for _, k := range []string{"Ingress", "HTTPRoute", "PodDisruptionBudget", "ServiceMonitor"} {
		if find(docs, k) != nil {
			t.Errorf("%s ne doit pas être rendu par défaut", k)
		}
	}
}

func TestOIDCRequiresClientID(t *testing.T) {
	if _, err := render(t); err == nil || !strings.Contains(err.Error(), "auth.oidc.clientID") {
		t.Fatalf("attendu un refus sans clientID, reçu %v", err)
	}
}

func TestOIDCRequiresAPublicURL(t *testing.T) {
	if _, err := render(t, "--set", "auth.oidc.clientID=atlas"); err == nil || !strings.Contains(err.Error(), "publicURL") {
		t.Fatalf("attendu un refus sans URL publique, reçu %v", err)
	}
	docs := mustRender(t, oidc...)
	env := get(find(docs, "Deployment"), "spec.template.spec.containers.0.env").([]any)
	if !hasEnv(env, "ATLAS_PUBLIC_URL", "https://atlas.corp") || !hasEnv(env, "ATLAS_OIDC_SCOPES", "openid,email,profile") {
		t.Errorf("env = %v", env)
	}
}

func TestAuthNoneIsRefusedWhenExposed(t *testing.T) {
	for _, exposure := range [][]string{
		{"--set", "ingress.enabled=true"},
		{"--set", "httpRoute.enabled=true", "--set", "httpRoute.parentRefs[0].name=gw"},
	} {
		_, err := render(t, append([]string{"--set", "auth.mode=none"}, exposure...)...)
		if err == nil || !strings.Contains(err.Error(), "auth.mode=none") {
			t.Errorf("%v : attendu un refus, reçu %v", exposure, err)
		}
	}
	if _, err := render(t, "--set", "auth.mode=none"); err != nil {
		t.Errorf("auth.mode=none sans exposition doit passer : %v", err)
	}
}

func TestClusterRoleMatchesSpec(t *testing.T) {
	role := find(mustRender(t, oidc...), "ClusterRole")
	rules := get(role, "rules").([]any)
	allowedWrites := map[string]bool{"impersonate": true}
	for _, r := range rules {
		res := toStrings(get(r, "resources"))
		for _, verb := range toStrings(get(r, "verbs")) {
			switch verb {
			case "get", "list", "watch":
			case "create":
				if len(res) != 1 || res[0] != "subjectaccessreviews" {
					t.Errorf("create autorisé sur %v", res)
				}
			default:
				if !allowedWrites[verb] {
					t.Errorf("verbe %q interdit par la spec sur %v", verb, res)
				}
			}
		}
		for _, x := range res {
			if x == "secrets" || x == "configmaps" || x == "pods/exec" || x == "*" {
				t.Errorf("ressource %q interdite par la spec", x)
			}
		}
	}

	restricted := find(mustRender(t, append(oidc, "--set", "rbac.impersonate.groups={oidc:sre,oidc:dev}")...), "ClusterRole")
	if names := toStrings(get(restricted, "rules.1.resourceNames")); len(names) != 2 || names[0] != "oidc:sre" {
		t.Errorf("resourceNames des groupes = %v", names)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestPodSecurity(t *testing.T) {
	dep := find(mustRender(t, oidc...), "Deployment")
	pod := get(dep, "spec.template.spec")
	if get(pod, "securityContext.runAsNonRoot") != true || get(pod, "securityContext.seccompProfile.type") != "RuntimeDefault" {
		t.Errorf("securityContext du pod = %v", get(pod, "securityContext"))
	}
	c := get(pod, "containers.0")
	if get(c, "securityContext.readOnlyRootFilesystem") != true || get(c, "securityContext.allowPrivilegeEscalation") != false ||
		get(c, "securityContext.capabilities.drop.0") != "ALL" {
		t.Errorf("securityContext du container = %v", get(c, "securityContext"))
	}
	if get(c, "readinessProbe.httpGet.path") != "/readyz" || get(c, "livenessProbe.httpGet.path") != "/healthz" {
		t.Error("probes manquantes")
	}
	if get(c, "resources.requests.cpu") != "100m" || get(c, "resources.limits.memory") != "512Mi" {
		t.Errorf("ressources = %v", get(c, "resources"))
	}
}

func TestHighAvailability(t *testing.T) {
	docs := mustRender(t, append(oidc, "--set", "replicaCount=2")...)
	if find(docs, "PodDisruptionBudget") == nil {
		t.Error("PDB attendu à 2 replicas")
	}
	if get(find(docs, "Deployment"), "spec.template.spec.affinity.podAntiAffinity") == nil {
		t.Error("anti-affinité attendue à 2 replicas")
	}
}

func TestIngressAndHTTPRoute(t *testing.T) {
	docs := mustRender(t, append(oidc[:4:4], "--set", "ingress.enabled=true", "--set", "ingress.host=atlas.corp",
		"--set", "ingress.certManager.clusterIssuer=letsencrypt")...)
	ing := find(docs, "Ingress")
	ann := get(ing, "metadata.annotations").(map[string]any)
	if ann["nginx.ingress.kubernetes.io/proxy-read-timeout"] != "3600" || ann["cert-manager.io/cluster-issuer"] != "letsencrypt" {
		t.Errorf("annotations = %v", ann)
	}
	if get(ing, "spec.tls.0.secretName") != "atlas-cluster-atlas-tls" {
		t.Errorf("tls = %v", get(ing, "spec.tls"))
	}
	env := get(find(docs, "Deployment"), "spec.template.spec.containers.0.env").([]any)
	if !hasEnv(env, "ATLAS_PUBLIC_URL", "https://atlas.corp") {
		t.Error("ATLAS_PUBLIC_URL attendu https://atlas.corp")
	}

	route := find(mustRender(t, append(oidc, "--set", "httpRoute.enabled=true", "--set", "httpRoute.parentRefs[0].name=gw",
		"--set", "httpRoute.hostnames[0]=atlas.corp")...), "HTTPRoute")
	if get(route, "spec.rules.0.timeouts.request") != "3600s" || get(route, "spec.parentRefs.0.name") != "gw" {
		t.Errorf("httproute = %v", get(route, "spec"))
	}
}

func hasEnv(env []any, name, value string) bool {
	for _, e := range env {
		if get(e, "name") == name && get(e, "value") == value {
			return true
		}
	}
	return false
}

func TestNetworkPolicy(t *testing.T) {
	np := find(mustRender(t, oidc...), "NetworkPolicy")
	egress := get(np, "spec.egress").([]any)
	if len(egress) != 3 {
		t.Fatalf("egress attendu : DNS, API server, issuer OIDC ; reçu %v", egress)
	}
	// Sans cluster (helm template), repli sur 0.0.0.0/0 pour l'API server.
	if get(egress[1], "to.0.ipBlock.cidr") != "0.0.0.0/0" || get(egress[1], "ports.1.port") != float64(6443) {
		t.Errorf("egress API server = %v", egress[1])
	}
	labels, _ := get(np, "spec.ingress.0.from.0.namespaceSelector.matchLabels").(map[string]any)
	if labels["kubernetes.io/metadata.name"] != "ingress-nginx" {
		t.Errorf("entrée HTTP autorisée depuis %v, attendu le namespace ingress-nginx", labels)
	}

	fixed := find(mustRender(t, append(oidc, "--set", "networkPolicy.apiServer.cidrs={172.18.0.2/32}")...), "NetworkPolicy")
	if get(fixed, "spec.egress.1.to.0.ipBlock.cidr") != "172.18.0.2/32" {
		t.Errorf("CIDR imposé ignoré : %v", get(fixed, "spec.egress.1"))
	}
	none := find(mustRender(t, "--set", "auth.mode=none"), "NetworkPolicy")
	if n := len(get(none, "spec.egress").([]any)); n != 2 {
		t.Errorf("sans OIDC, pas de sortie vers l'issuer : %d règles", n)
	}
}

func TestCookieKeyIsBase64Text(t *testing.T) {
	sec := find(mustRender(t, oidc...), "Secret")
	raw, err := base64.StdEncoding.DecodeString(get(sec, "data.cookie-key").(string))
	if err != nil {
		t.Fatal(err)
	}
	key, err := base64.StdEncoding.DecodeString(string(raw))
	if err != nil || len(key) != 32 {
		t.Errorf("cookie-key doit être le base64 de 32 octets (passé en variable d'environnement) : %q", raw)
	}
}

func TestExistingSecret(t *testing.T) {
	docs := mustRender(t, "--set", "auth.oidc.clientID=atlas", "--set", "auth.oidc.existingSecret=mine", "--set", "publicURL=https://a")
	if find(docs, "Secret") != nil {
		t.Error("aucun Secret ne doit être créé avec existingSecret")
	}
	env := get(find(docs, "Deployment"), "spec.template.spec.containers.0.env").([]any)
	for _, e := range env {
		if get(e, "name") == "ATLAS_COOKIE_KEY" && get(e, "valueFrom.secretKeyRef.name") != "mine" {
			t.Errorf("cookie-key lu dans %v", get(e, "valueFrom"))
		}
	}
}

func TestSchemaRejectsUnknownAuthMode(t *testing.T) {
	if _, err := render(t, "--set", "auth.mode=basic"); err == nil {
		t.Error("auth.mode=basic aurait dû être refusé par le schéma")
	}
}
