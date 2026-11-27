package gcplocaltest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	serviceusage "cloud.google.com/go/serviceusage/apiv1"
	"cloud.google.com/go/serviceusage/apiv1/serviceusagepb"
	"github.com/GuitarWag/gcp-local/tests/go/testutil"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	longrunningpb "google.golang.org/genproto/googleapis/longrunning"
	"google.golang.org/protobuf/proto"
)

// newSUClient opens a REST Service Usage client against the emulator.
func newSUClient(t *testing.T, em *testutil.Emulator) *serviceusage.Client {
	t.Helper()
	client, err := serviceusage.NewRESTClient(context.Background(),
		option.WithEndpoint("http://"+em.Host),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("serviceusage client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func suService(t *testing.T, client *serviceusage.Client, service string) *serviceusagepb.Service {
	t.Helper()
	svc, err := client.GetService(context.Background(), &serviceusagepb.GetServiceRequest{
		Name: fmt.Sprintf("projects/%s/services/%s", project, service),
	})
	if err != nil {
		t.Fatalf("get %s: %v", service, err)
	}
	return svc
}

// suOperation fetches the raw LRO and unmarshals its response into resp.
func suOperation(t *testing.T, client *serviceusage.Client, opName string, resp proto.Message) *longrunningpb.Operation {
	t.Helper()
	raw, err := client.GetOperation(context.Background(), &longrunningpb.GetOperationRequest{Name: opName})
	if err != nil {
		t.Fatalf("get operation %s: %v", opName, err)
	}
	if !raw.GetDone() {
		t.Fatalf("operation %s: done false", opName)
	}
	if err := raw.GetResponse().UnmarshalTo(resp); err != nil {
		t.Fatalf("operation %s: unmarshal response: %v", opName, err)
	}
	return raw
}

func suEnable(t *testing.T, client *serviceusage.Client, service string) *serviceusagepb.EnableServiceResponse {
	t.Helper()
	op, err := client.EnableService(context.Background(), &serviceusagepb.EnableServiceRequest{
		Name: "projects/" + project + "/services/" + service,
	})
	if err != nil {
		t.Fatalf("enable %s: %v", service, err)
	}
	var resp serviceusagepb.EnableServiceResponse
	suOperation(t, client, op.Name(), &resp)
	if resp.GetService().GetState() != serviceusagepb.State_ENABLED {
		t.Errorf("enable %s: response state %v, want ENABLED", service, resp.GetService().GetState())
	}
	if got := suService(t, client, service).GetState(); got != serviceusagepb.State_ENABLED {
		t.Errorf("enable %s: state after %v, want ENABLED", service, got)
	}
	return &resp
}

func TestServiceUsageListServices(t *testing.T) {
	em := testutil.Start(t)
	client := newSUClient(t, em)

	it := client.ListServices(context.Background(), &serviceusagepb.ListServicesRequest{
		Parent:   "projects/" + project,
		PageSize: 200,
	})
	var got []*serviceusagepb.Service
	for {
		svc, err := it.Next()
		if err != nil {
			break
		}
		got = append(got, svc)
	}
	if len(got) != 19 {
		t.Fatalf("expected 19 services, got %d", len(got))
	}
	for i, svc := range got {
		if svc.GetState() != serviceusagepb.State_ENABLED {
			t.Errorf("%s: state %v, want ENABLED", svc.GetName(), svc.GetState())
		}
		if svc.GetConfig().GetName() == "" || svc.GetConfig().GetTitle() == "" {
			t.Errorf("%s: missing config name or title", svc.GetName())
		}
		if i > 0 && got[i-1].GetName() >= svc.GetName() {
			t.Errorf("catalog not sorted: %s after %s", svc.GetName(), got[i-1].GetName())
		}
	}
	byName := map[string]*serviceusagepb.Service{}
	for _, svc := range got {
		byName[svc.GetConfig().GetName()] = svc
	}
	if s := byName["pubsub.googleapis.com"]; s == nil || s.GetConfig().GetTitle() != "Cloud Pub/Sub API" {
		t.Errorf("pubsub title: %v", s)
	}
	if s := byName["redis.googleapis.com"]; s == nil || s.GetConfig().GetTitle() != "Google Cloud Memorystore for Redis API" {
		t.Errorf("redis title: %v", s)
	}
}

func TestServiceUsageGetService(t *testing.T) {
	em := testutil.Start(t)
	client := newSUClient(t, em)
	svc := suService(t, client, "pubsub.googleapis.com")
	if got, want := svc.GetName(), "projects/"+project+"/services/pubsub.googleapis.com"; got != want {
		t.Errorf("name: %s, want %s", got, want)
	}
	if got, want := svc.GetParent(), "projects/"+project; got != want {
		t.Errorf("parent: %s, want %s", got, want)
	}
	if svc.GetState() != serviceusagepb.State_ENABLED {
		t.Errorf("state: %v, want ENABLED", svc.GetState())
	}

	_, err := client.GetService(context.Background(), &serviceusagepb.GetServiceRequest{
		Name: "projects/" + project + "/services/nosuch.googleapis.com",
	})
	var apiErr *googleapi.Error
	ok := errors.As(err, &apiErr)
	if !ok {
		t.Fatalf("unknown service: want googleapi.Error, got %v", err)
	}
	if apiErr.Code != 404 {
		t.Errorf("unknown service: code %d, want 404", apiErr.Code)
	}
}

func TestServiceUsageEnableService(t *testing.T) {
	em := testutil.Start(t)
	client := newSUClient(t, em)

	suEnable(t, client, "logging.googleapis.com")
	suEnable(t, client, "logging.googleapis.com")
}

func TestServiceUsageDisableService(t *testing.T) {
	em := testutil.Start(t)
	client := newSUClient(t, em)
	const svc = "bigquery.googleapis.com"

	suEnable(t, client, svc)

	op, err := client.DisableService(context.Background(), &serviceusagepb.DisableServiceRequest{
		Name: "projects/" + project + "/services/" + svc,
	})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	var resp serviceusagepb.DisableServiceResponse
	suOperation(t, client, op.Name(), &resp)
	if resp.GetService().GetState() != serviceusagepb.State_DISABLED {
		t.Errorf("disable: response state %v, want DISABLED", resp.GetService().GetState())
	}
	if got := suService(t, client, svc).GetState(); got != serviceusagepb.State_DISABLED {
		t.Errorf("state after disable: %v, want DISABLED", got)
	}

	_, err = client.DisableService(context.Background(), &serviceusagepb.DisableServiceRequest{
		Name: "projects/" + project + "/services/" + svc,
	})
	var apiErr *googleapi.Error
	ok := errors.As(err, &apiErr)
	if !ok {
		t.Fatalf("second disable: want googleapi.Error, got %v", err)
	}
	if apiErr.Code != 400 {
		t.Errorf("second disable: code %d, want 400", apiErr.Code)
	}
}

func TestServiceUsageBatchEnable(t *testing.T) {
	em := testutil.Start(t)
	client := newSUClient(t, em)

	op, err := client.BatchEnableServices(context.Background(), &serviceusagepb.BatchEnableServicesRequest{
		Parent:     "projects/" + project,
		ServiceIds: []string{"cloudtasks.googleapis.com", "cloudscheduler.googleapis.com"},
	})
	if err != nil {
		t.Fatalf("batch enable: %v", err)
	}
	var resp serviceusagepb.BatchEnableServicesResponse
	suOperation(t, client, op.Name(), &resp)
	if len(resp.GetServices()) != 2 {
		t.Fatalf("batch enable: %d services, want 2", len(resp.GetServices()))
	}
	for _, svc := range resp.GetServices() {
		if svc.GetState() != serviceusagepb.State_ENABLED {
			t.Errorf("batch enable: %s state %v, want ENABLED", svc.GetName(), svc.GetState())
		}
	}

	_, err = client.BatchEnableServices(context.Background(), &serviceusagepb.BatchEnableServicesRequest{
		Parent:     "projects/" + project,
		ServiceIds: []string{"cloudtasks.googleapis.com", "nosuch.googleapis.com"},
	})
	var apiErr *googleapi.Error
	ok := errors.As(err, &apiErr)
	if !ok {
		t.Fatalf("bad batch: want googleapi.Error, got %v", err)
	}
	if apiErr.Code != 404 {
		t.Errorf("bad batch: code %d, want 404", apiErr.Code)
	}
	if got := suService(t, client, "cloudtasks.googleapis.com").GetState(); got != serviceusagepb.State_ENABLED {
		t.Errorf("valid id changed by failed batch: %v", got)
	}
}

func TestServiceUsageBoltDBRestart(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "state.db")
	cfgPath := filepath.Join(tmp, "gcp-local.yaml")
	cfgBody := "project: local-project\nstate: boltdb\nstate_dir: " + dbPath + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o644); err != nil {
		t.Fatal(err)
	}

	em1 := startEmulator(t, cfgPath)
	client1 := newSUClient(t, em1.Emulator)
	suEnable(t, client1, "spanner.googleapis.com")
	op, err := client1.DisableService(context.Background(), &serviceusagepb.DisableServiceRequest{
		Name: "projects/" + project + "/services/spanner.googleapis.com",
	})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	var resp serviceusagepb.DisableServiceResponse
	suOperation(t, client1, op.Name(), &resp)
	stopEmulator(em1)

	em2 := startEmulator(t, cfgPath)
	client2 := newSUClient(t, em2.Emulator)
	if got := suService(t, client2, "spanner.googleapis.com").GetState(); got != serviceusagepb.State_DISABLED {
		t.Errorf("state after restart: %v, want DISABLED", got)
	}
}
