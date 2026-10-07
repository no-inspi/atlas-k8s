package kube

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

func pvFixtures() []runtime.Object {
	return append(netFixtures(),
		pvObj("pv-bound", corev1.VolumeBound, "prod/data"), // PVC data : netFixtures
		pvObj("pv-released", corev1.VolumeReleased, "prod/old"),
		pvObj("pv-free", corev1.VolumeAvailable, ""),
	)
}

func TestOrphanPersistentVolumes(t *testing.T) {
	client, sk := startSource(t, pvFixtures()...)
	if _, ok := sk.get(stream.KindPersistentVolume, "pv-bound"); ok {
		t.Error("PV lié à un PVC existant publié")
	}
	o, ok := sk.get(stream.KindPersistentVolume, "pv-released")
	if pv, _ := o.(model.PersistentVolume); !ok || pv.ClaimRef != "prod/old" || pv.Capacity != 10<<30 || pv.Phase != "Released" {
		t.Errorf("pv-released = %v %+v", ok, o)
	}
	if _, ok := sk.get(stream.KindPersistentVolume, "pv-free"); !ok {
		t.Error("PV Available absent")
	}

	// Le PVC disparaît : son PV, resté Bound, devient orphelin.
	if err := client.CoreV1().PersistentVolumeClaims("prod").Delete(context.Background(), "data", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "PV orphelin publié", func() bool { _, ok := sk.get(stream.KindPersistentVolume, "pv-bound"); return ok })

	// Le PVC revient (même UID que le claimRef, ici aucun) : le PV est de nouveau lié.
	if _, err := client.CoreV1().PersistentVolumeClaims("prod").Create(context.Background(),
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "prod"}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "PV re-masqué", func() bool { _, ok := sk.get(stream.KindPersistentVolume, "pv-bound"); return !ok })

	// Le PV est supprimé : retiré du flux.
	if err := client.CoreV1().PersistentVolumes().Delete(context.Background(), "pv-free", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "PV retiré", func() bool { _, ok := sk.get(stream.KindPersistentVolume, "pv-free"); return !ok })
}

// Un PVC recréé sous le même nom (autre UID) ne réclame plus l'ancien PV.
func TestRecreatedClaimOrphansItsVolume(t *testing.T) {
	pv := pvObj("pv-old", corev1.VolumeBound, "prod/data")
	pv.Spec.ClaimRef.UID = "uid-1"
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "prod", UID: "uid-1"}}
	client, sk := startSource(t, append(fixtures(), pvc, pv)...)
	if _, ok := sk.get(stream.KindPersistentVolume, "pv-old"); ok {
		t.Fatal("PV publié alors que son PVC (même UID) existe")
	}
	ctx := context.Background()
	if err := client.CoreV1().PersistentVolumeClaims("prod").Delete(ctx, "data", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	pvc2 := pvc.DeepCopy()
	pvc2.UID = "uid-2"
	pvc2.ResourceVersion = ""
	if _, err := client.CoreV1().PersistentVolumeClaims("prod").Create(ctx, pvc2, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "PV orphelin après recréation", func() bool { _, ok := sk.get(stream.KindPersistentVolume, "pv-old"); return ok })
}

func TestForbiddenPersistentVolumesAreDisabled(t *testing.T) {
	client := fake.NewClientset(pvFixtures()...)
	client.PrependReactor("list", "persistentvolumes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "persistentvolumes"}, "", errors.New("refusé"))
	})
	_, sk := startSourceWith(t, client, Options{})
	if _, ok := sk.get(stream.KindPersistentVolume, "pv-released"); ok {
		t.Error("PV publié malgré le refus")
	}
	if _, ok := sk.get(stream.KindVolume, "prod/data"); !ok {
		t.Error("les PVC doivent rester publiés")
	}
}

func TestForbiddenClaimsPublishOnlyOrphanPhases(t *testing.T) {
	client := fake.NewClientset(pvFixtures()...)
	client.PrependReactor("list", "persistentvolumeclaims", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "persistentvolumeclaims"}, "", errors.New("refusé"))
	})
	_, sk := startSourceWith(t, client, Options{})
	if _, ok := sk.get(stream.KindPersistentVolume, "pv-bound"); ok {
		t.Error("PV Bound publié sans pouvoir lire les PVC")
	}
	if _, ok := sk.get(stream.KindPersistentVolume, "pv-released"); !ok {
		t.Error("PV Released absent")
	}
}
