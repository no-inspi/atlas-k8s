package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPodJSONUsesFrontFieldNames(t *testing.T) {
	p := Pod{UID: "u1", Name: "api-7f", Namespace: "production", NodeName: "n1",
		DisplayStatus: "CrashLoopBackOff", Owner: OwnerRef{Kind: "Deployment", Name: "api"}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"uid":"u1"`, `"nodeName":"n1"`, `"displayStatus":"CrashLoopBackOff"`,
		`"owner":{"kind":"Deployment","name":"api"}`, `"qosClass"`, `"createdAt"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON du pod sans %s : %s", want, b)
		}
	}
}

func TestWorkloadOmitsArgoWhenAbsent(t *testing.T) {
	b, _ := json.Marshal(Workload{Kind: "Deployment", Name: "api", Namespace: "production"})
	if strings.Contains(string(b), "argocd") {
		t.Errorf("clé argocd inattendue : %s", b)
	}
	b, _ = json.Marshal(Workload{Kind: "Deployment", Name: "api", Argo: &ArgoInfo{Application: "prod-apps", SyncStatus: "Synced"}})
	if !strings.Contains(string(b), `"argocd":{"application":"prod-apps","syncStatus":"Synced"}`) {
		t.Errorf("argocd mal sérialisé : %s", b)
	}
}

func TestKeys(t *testing.T) {
	if got := WorkloadKey(Workload{Kind: "StatefulSet", Namespace: "db", Name: "pg"}); got != "StatefulSet/db/pg" {
		t.Errorf("WorkloadKey = %q", got)
	}
	if got := PodKey(Pod{UID: "abc"}); got != "abc" {
		t.Errorf("PodKey = %q", got)
	}
	if got := NodeKey(Node{Name: "n1"}); got != "n1" {
		t.Errorf("NodeKey = %q", got)
	}
}
