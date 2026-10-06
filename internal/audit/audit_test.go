package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestRecordWritesOneJSONLine(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	l.now = func() time.Time { return time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC) }
	l.Record(Entry{User: "bob@example.com", Groups: []string{"oidc:dev"}, Verb: "delete", Resource: "pods",
		Namespace: "production", Name: "api-1", Duration: 42 * time.Millisecond}, nil)
	l.Record(Entry{User: "alice", Verb: "delete", Resource: "pods", Namespace: "kube-system", Name: "x"},
		apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "x", errors.New("non")))

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("attendu 2 lignes, reçu %d : %s", len(lines), buf.String())
	}
	var first, second map[string]any
	_ = json.Unmarshal(lines[0], &first)
	_ = json.Unmarshal(lines[1], &second)
	if first["audit"] != true || first["time"] != "2026-10-06T09:00:00Z" || first["user"] != "bob@example.com" ||
		first["verb"] != "delete" || first["resource"] != "pods" || first["namespace"] != "production" ||
		first["name"] != "api-1" || first["result"] != "success" || first["durationMs"] != float64(42) {
		t.Errorf("première ligne = %v", first)
	}
	if g, _ := first["groups"].([]any); len(g) != 1 || g[0] != "oidc:dev" {
		t.Errorf("groupes = %v", first["groups"])
	}
	if second["result"] != "forbidden" || second["error"] == nil {
		t.Errorf("seconde ligne = %v", second)
	}
}

func TestResult(t *testing.T) {
	if Result(nil) != "success" || Result(errors.New("x")) != "failure" {
		t.Error("résultats inattendus")
	}
}
