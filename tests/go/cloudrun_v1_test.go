package gcplocaltest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	run "google.golang.org/api/run/v1"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// newRunV1Client opens the run/v1 REST client against the emulator. The
// endpoint stays at the host root: the regional templates already carry the
// v1 prefix, and the namespaced templates carry apis/serving.knative.dev/v1.
func newRunV1Client(t *testing.T, em *testutil.Emulator) *run.APIService {
	t.Helper()
	client, err := run.NewService(context.Background(),
		option.WithoutAuthentication(),
		option.WithEndpoint("http://"+em.Host),
	)
	if err != nil {
		t.Fatalf("run v1 client: %v", err)
	}
	return client
}

func runV1Service(name, image string) *run.Service {
	return &run.Service{
		ApiVersion: "serving.knative.dev/v1",
		Kind:       "Service",
		Metadata: &run.ObjectMeta{
			Name:      name,
			Namespace: project,
		},
		Spec: &run.ServiceSpec{
			Template: &run.RevisionTemplate{
				Spec: &run.RevisionSpec{
					Containers: []*run.Container{{Image: image}},
				},
			},
		},
	}
}

func runV1Condition(svc *run.Service, condType string) *run.GoogleCloudRunV1Condition {
	if svc == nil || svc.Status == nil {
		return nil
	}
	for _, c := range svc.Status.Conditions {
		if c.Type == condType {
			return c
		}
	}
	return nil
}

// assertReadyService checks the fields the spec pins on a v1 Service
// response: generation, observedGeneration, the three Ready conditions,
// revision names, traffic and the server-assigned metadata.
func assertReadyService(t *testing.T, svc *run.Service, name string, wantGen int64) {
	t.Helper()
	if svc.ApiVersion != "serving.knative.dev/v1" {
		t.Errorf("apiVersion: %q, want serving.knative.dev/v1", svc.ApiVersion)
	}
	if svc.Kind != "Service" {
		t.Errorf("kind: %q, want Service", svc.Kind)
	}
	if svc.Metadata == nil {
		t.Fatal("metadata missing")
	}
	if svc.Metadata.Name != name {
		t.Errorf("metadata.name: %q, want %q", svc.Metadata.Name, name)
	}
	if svc.Metadata.Namespace != project {
		t.Errorf("metadata.namespace: %q, want %q", svc.Metadata.Namespace, project)
	}
	if svc.Metadata.Uid == "" {
		t.Error("metadata.uid empty")
	}
	if svc.Metadata.CreationTimestamp == "" {
		t.Error("metadata.creationTimestamp empty")
	}
	if svc.Metadata.Generation != wantGen {
		t.Errorf("metadata.generation: %d, want %d", svc.Metadata.Generation, wantGen)
	}
	if svc.Status == nil {
		t.Fatal("status missing")
	}
	if svc.Status.Url == "" {
		t.Error("status.url empty")
	}
	if svc.Status.ObservedGeneration != wantGen {
		t.Errorf("status.observedGeneration: %d, want %d", svc.Status.ObservedGeneration, wantGen)
	}
	for _, condType := range []string{"Ready", "ConfigurationsReady", "RoutesReady"} {
		c := runV1Condition(svc, condType)
		if c == nil {
			t.Errorf("condition %s missing", condType)
			continue
		}
		if c.Status != "True" {
			t.Errorf("condition %s: status %q, want True", condType, c.Status)
		}
	}
	rev := fmt.Sprintf("%s-%05d", name, wantGen)
	if svc.Status.LatestCreatedRevisionName != rev {
		t.Errorf("latestCreatedRevisionName: %q, want %q", svc.Status.LatestCreatedRevisionName, rev)
	}
	if svc.Status.LatestReadyRevisionName != rev {
		t.Errorf("latestReadyRevisionName: %q, want %q", svc.Status.LatestReadyRevisionName, rev)
	}
	if len(svc.Status.Traffic) != 1 {
		t.Fatalf("status.traffic: %d entries, want 1", len(svc.Status.Traffic))
	}
	if svc.Status.Traffic[0].Percent != 100 {
		t.Errorf("traffic percent: %d, want 100", svc.Status.Traffic[0].Percent)
	}
	if !svc.Status.Traffic[0].LatestRevision {
		t.Error("traffic latestRevision: false, want true")
	}
}

func TestCloudRunV1ServicesCreate(t *testing.T) {
	em := testutil.Start(t)
	client := newRunV1Client(t, em)

	svc, err := client.Projects.Locations.Services.Create(
		"projects/"+project+"/locations/us-central1",
		runV1Service("v1-create", "busybox:1.0"),
	).Do()
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	assertReadyService(t, svc, "v1-create", 1)
	if img := svc.Spec.Template.Spec.Containers[0].Image; img != "busybox:1.0" {
		t.Errorf("spec echo: image %q, want busybox:1.0", img)
	}

	got, err := client.Projects.Locations.Services.Get(
		"projects/" + project + "/locations/us-central1/services/v1-create",
	).Do()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	assertReadyService(t, got, "v1-create", 1)
	if got.Metadata.Uid != svc.Metadata.Uid {
		t.Errorf("uid after get: %q, want %q", got.Metadata.Uid, svc.Metadata.Uid)
	}
}

func TestCloudRunV1ServicesReplaceService(t *testing.T) {
	em := testutil.Start(t)
	client := newRunV1Client(t, em)

	name := "projects/" + project + "/locations/us-central1/services/v1-replace"
	if _, err := client.Projects.Locations.Services.Create(
		"projects/"+project+"/locations/us-central1",
		runV1Service("v1-replace", "busybox:1.0"),
	).Do(); err != nil {
		t.Fatalf("create: %v", err)
	}

	svc, err := client.Projects.Locations.Services.ReplaceService(
		name, runV1Service("v1-replace", "busybox:2.0"),
	).Do()
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	assertReadyService(t, svc, "v1-replace", 2)
	if img := svc.Spec.Template.Spec.Containers[0].Image; img != "busybox:2.0" {
		t.Errorf("spec echo after replace: image %q, want busybox:2.0", img)
	}

	got, err := client.Projects.Locations.Services.Get(name).Do()
	if err != nil {
		t.Fatalf("get after replace: %v", err)
	}
	if got.Metadata.Generation != 2 {
		t.Errorf("generation after replace: %d, want 2", got.Metadata.Generation)
	}
	if img := got.Spec.Template.Spec.Containers[0].Image; img != "busybox:2.0" {
		t.Errorf("image after replace: %q, want busybox:2.0", img)
	}
}

func TestCloudRunV1CreateDuplicate(t *testing.T) {
	em := testutil.Start(t)
	client := newRunV1Client(t, em)

	parent := "projects/" + project + "/locations/us-central1"
	if _, err := client.Projects.Locations.Services.Create(
		parent, runV1Service("v1-dup", "busybox:1.0"),
	).Do(); err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err := client.Projects.Locations.Services.Create(
		parent, runV1Service("v1-dup", "busybox:1.0"),
	).Do()
	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("duplicate create: want googleapi.Error, got %v", err)
	}
	if apiErr.Code != 409 {
		t.Errorf("duplicate create: code %d, want 409", apiErr.Code)
	}
}

func TestCloudRunV1DeleteAndGet(t *testing.T) {
	em := testutil.Start(t)
	client := newRunV1Client(t, em)

	name := "projects/" + project + "/locations/us-central1/services/v1-gone"
	if _, err := client.Projects.Locations.Services.Create(
		"projects/"+project+"/locations/us-central1",
		runV1Service("v1-gone", "busybox:1.0"),
	).Do(); err != nil {
		t.Fatalf("create: %v", err)
	}

	st, err := client.Projects.Locations.Services.Delete(name).Do()
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if st == nil {
		t.Fatal("delete: nil status")
	}

	cases := []struct {
		name string
		op   func() error
	}{
		{"get after delete", func() error {
			_, err := client.Projects.Locations.Services.Get(name).Do()
			return err
		}},
		{"second delete", func() error {
			_, err := client.Projects.Locations.Services.Delete(name).Do()
			return err
		}},
	}
	for _, tc := range cases {
		err := tc.op()
		var apiErr *googleapi.Error
		if !errors.As(err, &apiErr) {
			t.Errorf("%s: want googleapi.Error, got %v", tc.name, err)
			continue
		}
		if apiErr.Code != 404 {
			t.Errorf("%s: code %d, want 404", tc.name, apiErr.Code)
		}
	}
}

func TestCloudRunV1Operations(t *testing.T) {
	em := testutil.Start(t)
	client := newRunV1Client(t, em)

	parent := "projects/" + project + "/locations/us-central1"
	if _, err := client.Projects.Locations.Services.Create(
		parent, runV1Service("v1-ops", "busybox:1.0"),
	).Do(); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := client.Projects.Locations.Services.ReplaceService(
		parent+"/services/v1-ops", runV1Service("v1-ops", "busybox:2.0"),
	).Do(); err != nil {
		t.Fatalf("replace: %v", err)
	}

	list, err := client.Projects.Locations.Operations.List(parent).Do()
	if err != nil {
		t.Fatalf("operations list: %v", err)
	}
	if len(list.Operations) < 2 {
		t.Fatalf("operations list: %d records, want at least 2", len(list.Operations))
	}

	byGen := map[int64]*run.Service{}
	opNames := map[int64]string{}
	for _, op := range list.Operations {
		if !op.Done {
			t.Errorf("operation %s: done false, want true", op.Name)
			continue
		}
		if op.Name == "" {
			t.Error("operation name empty")
			continue
		}
		var svc run.Service
		if err := json.Unmarshal(op.Response, &svc); err != nil {
			t.Errorf("operation %s: unmarshal response: %v", op.Name, err)
			continue
		}
		byGen[svc.Metadata.Generation] = &svc
		opNames[svc.Metadata.Generation] = op.Name
	}
	created, ok := byGen[1]
	if !ok {
		t.Fatal("operations list: no record for generation 1")
	}
	assertReadyService(t, created, "v1-ops", 1)
	replaced, ok := byGen[2]
	if !ok {
		t.Fatal("operations list: no record for generation 2")
	}
	assertReadyService(t, replaced, "v1-ops", 2)

	op, err := client.Projects.Locations.Operations.Get(opNames[1]).Do()
	if err != nil {
		t.Fatalf("operations get: %v", err)
	}
	if !op.Done {
		t.Error("operations get: done false, want true")
	}
	var svc run.Service
	if err := json.Unmarshal(op.Response, &svc); err != nil {
		t.Fatalf("operations get: unmarshal response: %v", err)
	}
	assertReadyService(t, &svc, "v1-ops", 1)
}

func TestCloudRunV1NamespacedForm(t *testing.T) {
	em := testutil.Start(t)
	client := newRunV1Client(t, em)

	nsParent := "apis/serving.knative.dev/v1/namespaces/" + project
	nsName := nsParent + "/services/v1-ns"

	svc, err := client.Projects.Locations.Services.Create(
		nsParent, runV1Service("v1-ns", "busybox:1.0"),
	).Do()
	if err != nil {
		t.Fatalf("namespaced create: %v", err)
	}
	assertReadyService(t, svc, "v1-ns", 1)

	got, err := client.Projects.Locations.Services.Get(nsName).Do()
	if err != nil {
		t.Fatalf("namespaced get: %v", err)
	}
	assertReadyService(t, got, "v1-ns", 1)

	regional, err := client.Projects.Locations.Services.Get(
		"projects/" + project + "/locations/us-central1/services/v1-ns",
	).Do()
	if err != nil {
		t.Fatalf("regional get: %v", err)
	}
	if regional.Metadata.Uid != svc.Metadata.Uid {
		t.Errorf("regional uid: %q, want %q", regional.Metadata.Uid, svc.Metadata.Uid)
	}
	if regional.Metadata.Generation != svc.Metadata.Generation {
		t.Errorf("regional generation: %d, want %d", regional.Metadata.Generation, svc.Metadata.Generation)
	}
}
