package gcplocaltest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"

	longrunningpb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	workflows "cloud.google.com/go/workflows/apiv1"
	"cloud.google.com/go/workflows/apiv1/workflowspb"
	"github.com/GuitarWag/gcp-local/tests/go/testutil"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const workflowsParent = "projects/" + project + "/locations/us-central1"

var revisionIDPattern = regexp.MustCompile(`^[0-9]{6}-[0-9a-z]{3}$`)

func newWorkflowsClient(t *testing.T, em *testutil.Emulator) *workflows.Client {
	t.Helper()
	ctx := context.Background()
	client, err := workflows.NewRESTClient(ctx,
		option.WithEndpoint("http://"+em.Host),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("workflows REST client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func workflowsSource(x string) string {
	return "main:\n  steps:\n    - init:\n        assign:\n          - " + x + ": 1\n"
}

func mustCreateWorkflow(t *testing.T, client *workflows.Client, id, source string) *workflowspb.Workflow {
	t.Helper()
	op, err := client.CreateWorkflow(context.Background(), &workflowspb.CreateWorkflowRequest{
		Parent:     workflowsParent,
		WorkflowId: id,
		Workflow: &workflowspb.Workflow{
			SourceCode: &workflowspb.Workflow_SourceContents{SourceContents: source},
		},
	})
	if err != nil {
		t.Fatalf("create workflow %s: %v", id, err)
	}
	wf, err := op.Wait(context.Background())
	if err != nil {
		t.Fatalf("create workflow %s wait: %v", id, err)
	}
	return wf
}

func TestWorkflowsCRUDLifecycle(t *testing.T) {
	em := testutil.Start(t)
	client := newWorkflowsClient(t, em)
	ctx := context.Background()

	wf := mustCreateWorkflow(t, client, "life-a", workflowsSource("x"))
	wantName := workflowsParent + "/workflows/life-a"
	if wf.Name != wantName {
		t.Errorf("create name = %q, want %q", wf.Name, wantName)
	}
	if wf.State != workflowspb.Workflow_ACTIVE {
		t.Errorf("create state = %v, want ACTIVE", wf.State)
	}
	if !revisionIDPattern.MatchString(wf.RevisionId) {
		t.Errorf("revisionId %q does not match NNNNNN-xxx", wf.RevisionId)
	}
	if wf.CreateTime == nil || wf.UpdateTime == nil {
		t.Errorf("create timestamps missing: create=%v update=%v", wf.CreateTime, wf.UpdateTime)
	}

	got, err := client.GetWorkflow(ctx, &workflowspb.GetWorkflowRequest{Name: wantName})
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	if got.GetSourceContents() != workflowsSource("x") {
		t.Errorf("get sourceContents = %q, want the created source", got.GetSourceContents())
	}

	newRev := got.RevisionId
	upd, err := client.UpdateWorkflow(ctx, &workflowspb.UpdateWorkflowRequest{
		Workflow: &workflowspb.Workflow{
			Name:       wantName,
			SourceCode: &workflowspb.Workflow_SourceContents{SourceContents: workflowsSource("y")},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"source_contents"}},
	})
	if err != nil {
		t.Fatalf("patch workflow: %v", err)
	}
	patched, err := upd.Wait(ctx)
	if err != nil {
		t.Fatalf("patch wait: %v", err)
	}
	if patched.RevisionId == newRev {
		t.Errorf("patch kept revisionId %q, want a new revision", patched.RevisionId)
	}
	if !revisionIDPattern.MatchString(patched.RevisionId) {
		t.Errorf("patched revisionId %q does not match NNNNNN-xxx", patched.RevisionId)
	}

	// life-b is created after the patch, so a newest-first list starts with it.
	mustCreateWorkflow(t, client, "life-b", workflowsSource("z"))
	it := client.ListWorkflows(ctx, &workflowspb.ListWorkflowsRequest{
		Parent:   workflowsParent,
		PageSize: 1,
	})
	var order []string
	for {
		item, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			t.Fatalf("list workflows: %v", err)
		}
		order = append(order, item.Name)
	}
	if len(order) != 2 {
		t.Fatalf("list returned %d workflows, want 2 (%v)", len(order), order)
	}
	if order[0] != workflowsParent+"/workflows/life-b" || order[1] != wantName {
		t.Errorf("list order = %v, want newest first [life-b life-a]", order)
	}

	// Duplicate create: HTTP 200 with a done Operation whose error is
	// ALREADY_EXISTS. Never a direct HTTP 409 on create.
	dup, err := client.CreateWorkflow(ctx, &workflowspb.CreateWorkflowRequest{
		Parent:     workflowsParent,
		WorkflowId: "life-a",
		Workflow: &workflowspb.Workflow{
			SourceCode: &workflowspb.Workflow_SourceContents{SourceContents: workflowsSource("x")},
		},
	})
	if err != nil {
		t.Fatalf("duplicate create returned a transport error, want 200 + operation: %v", err)
	}
	if _, err := dup.Wait(ctx); status.Code(err) != codes.AlreadyExists {
		t.Errorf("duplicate create wait = %v, want code AlreadyExists", err)
	}

	del, err := client.DeleteWorkflow(ctx, &workflowspb.DeleteWorkflowRequest{Name: wantName})
	if err != nil {
		t.Fatalf("delete workflow: %v", err)
	}
	if err := del.Wait(ctx); err != nil {
		t.Fatalf("delete wait: %v", err)
	}
	if _, err := client.GetWorkflow(ctx, &workflowspb.GetWorkflowRequest{Name: wantName}); apiCode(err) != http.StatusNotFound {
		t.Errorf("get deleted workflow = %v, want 404", err)
	}

	cleanup, err := client.DeleteWorkflow(ctx, &workflowspb.DeleteWorkflowRequest{Name: workflowsParent + "/workflows/life-b"})
	if err != nil {
		t.Fatalf("delete life-b: %v", err)
	}
	if err := cleanup.Wait(ctx); err != nil {
		t.Fatalf("delete life-b wait: %v", err)
	}
}

func TestWorkflowsOperationsGetShape(t *testing.T) {
	em := testutil.Start(t)
	client := newWorkflowsClient(t, em)

	op, err := client.CreateWorkflow(context.Background(), &workflowspb.CreateWorkflowRequest{
		Parent:     workflowsParent,
		WorkflowId: "op-shape",
		Workflow: &workflowspb.Workflow{
			SourceCode: &workflowspb.Workflow_SourceContents{SourceContents: workflowsSource("x")},
		},
	})
	if err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	raw, err := client.GetOperation(context.Background(), &longrunningpb.GetOperationRequest{Name: op.Name()})
	if err != nil {
		t.Fatalf("operations.get: %v", err)
	}
	if !raw.Done {
		t.Errorf("operation %s not done", raw.Name)
	}
	resp := raw.GetResponse()
	if resp == nil {
		t.Fatalf("operation %s has no response", raw.Name)
	}
	if got := resp.GetTypeUrl(); got != "type.googleapis.com/google.cloud.workflows.v1.Workflow" {
		t.Errorf("response @type = %q, want the Workflow Any type", got)
	}
}

func TestWorkflowsListPaginationWire(t *testing.T) {
	em := testutil.Start(t)
	client := newWorkflowsClient(t, em)

	for _, id := range []string{"page-1", "page-2", "page-3"} {
		mustCreateWorkflow(t, client, id, workflowsSource("x"))
	}

	base := "http://" + em.Host + "/v1/" + workflowsParent + "/workflows"
	resp, body := doJSON(t, http.MethodGet, base+"?pageSize=2", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list page 1: %d %s", resp.StatusCode, body)
	}
	page1 := decodeList(t, body, "workflows")
	if len(page1) != 2 {
		t.Fatalf("page 1 returned %d items, want 2: %s", len(page1), body)
	}
	token := decodeString(t, body, "nextPageToken")
	if token == "" {
		t.Fatalf("page 1 has no nextPageToken: %s", body)
	}
	resp, body = doJSON(t, http.MethodGet, base+"?pageSize=2&pageToken="+token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list page 2: %d %s", resp.StatusCode, body)
	}
	page2 := decodeList(t, body, "workflows")
	if len(page2) != 1 {
		t.Fatalf("page 2 returned %d items, want 1: %s", len(page2), body)
	}
	if decodeString(t, body, "nextPageToken") != "" {
		t.Errorf("page 2 has a nextPageToken, want none: %s", body)
	}

	for _, id := range []string{"page-1", "page-2", "page-3"} {
		name := workflowsParent + "/workflows/" + id
		del, err := client.DeleteWorkflow(context.Background(), &workflowspb.DeleteWorkflowRequest{Name: name})
		if err != nil {
			t.Fatalf("delete %s: %v", id, err)
		}
		if err := del.Wait(context.Background()); err != nil {
			t.Fatalf("delete %s wait: %v", id, err)
		}
	}
}

func TestWorkflowsValidationAnd404(t *testing.T) {
	em := testutil.Start(t)
	client := newWorkflowsClient(t, em)
	ctx := context.Background()

	cases := []struct {
		name string
		id   string
		src  string
	}{
		{"bad id first char", "9bad", workflowsSource("x")},
		{"bad id illegal char", "bad_id!", workflowsSource("x")},
		{"bad id trailing hyphen", "bad-", workflowsSource("x")},
		{"unparseable yaml", "ok-id", "::: not yaml ::"},
		{"missing main", "ok-id", "steps:\n  - init: {}\n"},
		{"main without steps", "ok-id", "main: {}\n"},
		{"empty steps", "ok-id", "main:\n  steps: []\n"},
		{"source over 128KB", "ok-id", "main:\n  steps:\n    - init: '" + strings.Repeat("a", 140*1024) + "'\n"},
	}
	for _, tc := range cases {
		_, err := client.CreateWorkflow(ctx, &workflowspb.CreateWorkflowRequest{
			Parent:     workflowsParent,
			WorkflowId: tc.id,
			Workflow: &workflowspb.Workflow{
				SourceCode: &workflowspb.Workflow_SourceContents{SourceContents: tc.src},
			},
		})
		if apiCode(err) != http.StatusBadRequest {
			t.Errorf("%s: create = %v, want 400", tc.name, err)
		}
	}

	name := workflowsParent + "/workflows/nope"
	if _, err := client.GetWorkflow(ctx, &workflowspb.GetWorkflowRequest{Name: name}); apiCode(err) != http.StatusNotFound {
		t.Errorf("get unknown = %v, want 404", err)
	}
	if _, err := client.UpdateWorkflow(ctx, &workflowspb.UpdateWorkflowRequest{
		Workflow: &workflowspb.Workflow{
			Name:       name,
			SourceCode: &workflowspb.Workflow_SourceContents{SourceContents: workflowsSource("x")},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"source_contents"}},
	}); apiCode(err) != http.StatusNotFound {
		t.Errorf("patch unknown = %v, want 404", err)
	}
	if _, err := client.DeleteWorkflow(ctx, &workflowspb.DeleteWorkflowRequest{Name: name}); apiCode(err) != http.StatusNotFound {
		t.Errorf("delete unknown = %v, want 404", err)
	}
}

// apiCode extracts the HTTP status code that the REST client parsed from
// an error response. 0 means the error is not a wire error.
func apiCode(err error) int {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return 0
}

// decodeList extracts a JSON array field from a wire response body.
func decodeList(t *testing.T, body []byte, field string) []map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response: %v (%s)", err, body)
	}
	items, ok := out[field].([]any)
	if !ok {
		t.Fatalf("response has no %q list: %s", field, body)
	}
	list := make([]map[string]any, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("%q item is not an object: %s", field, body)
		}
		list = append(list, m)
	}
	return list
}

// decodeString extracts a JSON string field from a wire response body.
func decodeString(t *testing.T, body []byte, field string) string {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response: %v (%s)", err, body)
	}
	s, _ := out[field].(string)
	return s
}
