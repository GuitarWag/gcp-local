// Command apicoverage reports which real GCP API methods the emulator serves.
//
// It builds and starts gcp-local, then probes every REST method in the
// discovery documents and every gRPC method in the registered protos. A
// method counts as missing when the emulator answers with the router's
// plain-text 404 (REST) or codes.Unimplemented (gRPC). The probe only shows
// that a route exists, not that it behaves correctly.
//
//	cd tests/go && go run ./apicoverage > coverage.md
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	_ "cloud.google.com/go/bigtable/apiv2/bigtablepb"
	_ "cloud.google.com/go/firestore/apiv1/firestorepb"
	_ "cloud.google.com/go/pubsub/apiv1/pubsubpb"
	_ "cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	_ "cloud.google.com/go/spanner/apiv1/spannerpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/emptypb"
)

var restAPIs = [][2]string{
	{"storage", "v1"}, {"pubsub", "v1"}, {"secretmanager", "v1"}, {"cloudtasks", "v2"},
	{"cloudscheduler", "v1"}, {"cloudkms", "v1"}, {"logging", "v2"}, {"monitoring", "v3"},
	{"bigquery", "v2"}, {"run", "v2"}, {"cloudfunctions", "v2"}, {"sqladmin", "v1beta4"},
	{"iam", "v1"}, {"iamcredentials", "v1"}, {"workflows", "v1"},
}

var grpcServices = []protoreflect.FullName{
	"google.pubsub.v1.Publisher", "google.pubsub.v1.Subscriber",
	"google.firestore.v1.Firestore",
	"google.bigtable.v2.Bigtable", "google.bigtable.admin.v2.BigtableTableAdmin",
	"google.spanner.v1.Spanner", "google.spanner.admin.database.v1.DatabaseAdmin",
}

type result struct {
	api     string
	total   int
	missing []string
}

func main() {
	host := flag.String("host", "", "probe a running emulator instead of starting one")
	flag.Parse()
	if err := run(*host); err != nil {
		fmt.Fprintln(os.Stderr, "apicoverage:", err)
		os.Exit(1)
	}
}

func run(host string) error {
	if host == "" {
		h, stop, err := startEmulator()
		if err != nil {
			return err
		}
		defer stop()
		host = h
	}
	var results []result
	for _, a := range restAPIs {
		r, err := probeREST(host, a[0], a[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s %s: %v\n", a[0], a[1], err)
			continue
		}
		results = append(results, r)
	}
	gr, err := probeGRPC(host)
	if err != nil {
		return err
	}
	report(append(results, gr...))
	return nil
}

type discoveryDoc struct {
	ServicePath string              `json:"servicePath"`
	Resources   map[string]resource `json:"resources"`
}

type resource struct {
	Methods   map[string]method   `json:"methods"`
	Resources map[string]resource `json:"resources"`
}

type method struct {
	ID         string `json:"id"`
	HTTPMethod string `json:"httpMethod"`
	Path       string `json:"path"`
	FlatPath   string `json:"flatPath"`
}

func collect(rs map[string]resource, out *[]method) {
	for _, r := range rs {
		for _, m := range r.Methods {
			*out = append(*out, m)
		}
		collect(r.Resources, out)
	}
}

var pathParam = regexp.MustCompile(`\{\+?[^}]+\}`)

func probeREST(host, name, version string) (result, error) {
	doc, err := fetchDiscovery(name, version)
	if err != nil {
		return result{}, err
	}
	var methods []method
	collect(doc.Resources, &methods)

	res := result{api: name + " " + version + " (REST)", total: len(methods)}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, m := range methods {
		p := m.FlatPath
		if p == "" {
			p = m.Path
		}
		p = "/" + strings.TrimPrefix(doc.ServicePath+pathParam.ReplaceAllString(p, "probe"), "/")
		if !servedREST(client, host, m.HTTPMethod, p) {
			res.missing = append(res.missing, fmt.Sprintf("`%s` %s %s", m.ID, m.HTTPMethod, p))
		}
	}
	return res, nil
}

// fetchDiscovery tries the directory first; some APIs publish only on their own host.
func fetchDiscovery(name, version string) (discoveryDoc, error) {
	urls := []string{
		fmt.Sprintf("https://www.googleapis.com/discovery/v1/apis/%s/%s/rest", name, version),
		fmt.Sprintf("https://%s.googleapis.com/$discovery/rest?version=%s", name, version),
	}
	var last error
	for _, url := range urls {
		resp, err := http.Get(url)
		if err != nil {
			last = err
			continue
		}
		var doc discoveryDoc
		if resp.StatusCode == http.StatusOK {
			err = json.NewDecoder(resp.Body).Decode(&doc)
		} else {
			err = fmt.Errorf("discovery %s: %s", url, resp.Status)
		}
		_ = resp.Body.Close()
		if err == nil {
			return doc, nil
		}
		last = err
	}
	return discoveryDoc{}, last
}

func servedREST(client *http.Client, host, verb, path string) bool {
	var body io.Reader
	if verb == http.MethodPost || verb == http.MethodPut || verb == http.MethodPatch {
		body = strings.NewReader("{}")
	}
	req, err := http.NewRequest(verb, "http://"+host+path, body)
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	return resp.StatusCode != http.StatusNotFound || !strings.HasPrefix(string(b), "404 page not found")
}

func probeGRPC(host string) ([]result, error) {
	conn, err := grpc.NewClient(host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	var out []result
	for _, name := range grpcServices {
		d, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		sd := d.(protoreflect.ServiceDescriptor)
		res := result{api: string(name) + " (gRPC)", total: sd.Methods().Len()}
		for i := 0; i < sd.Methods().Len(); i++ {
			m := sd.Methods().Get(i)
			full := "/" + string(name) + "/" + string(m.Name())
			if status.Code(callGRPC(conn, full)) == codes.Unimplemented {
				res.missing = append(res.missing, "`"+string(m.Name())+"`")
			}
		}
		out = append(out, res)
	}
	return out, nil
}

// callGRPC sends one empty message on a stream, which suits every RPC shape.
func callGRPC(conn *grpc.ClientConn, full string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}, full)
	if err != nil {
		return err
	}
	if err := s.SendMsg(&emptypb.Empty{}); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	_ = s.CloseSend()
	return s.RecvMsg(&emptypb.Empty{})
}

func report(rs []result) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].api < rs[j].api })
	fmt.Println("# API coverage")
	fmt.Println()
	fmt.Println("A method counts as served when the emulator routes it. Served does not mean correct. Services that share a dispatcher can answer an unknown path with a JSON 404, so their counts can be too high.")
	fmt.Println()
	fmt.Println("| API | Served | Total |")
	fmt.Println("|---|---|---|")
	for _, r := range rs {
		fmt.Printf("| %s | %d | %d |\n", r.api, r.total-len(r.missing), r.total)
	}
	for _, r := range rs {
		if len(r.missing) == 0 {
			continue
		}
		sort.Strings(r.missing)
		fmt.Printf("\n## Missing: %s\n\n", r.api)
		for _, m := range r.missing {
			fmt.Println("- " + m)
		}
	}
}

func startEmulator() (string, func(), error) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", nil, fmt.Errorf("find repo root: %w", err)
	}
	dir, err := os.MkdirTemp("", "apicoverage-")
	if err != nil {
		return "", nil, err
	}
	bin := filepath.Join(dir, "gcp-local")
	build := exec.Command("go", "build", "-o", bin, "./cmd/gcp-local")
	build.Dir = strings.TrimSpace(string(root))
	if out, err := build.CombinedOutput(); err != nil {
		return "", nil, fmt.Errorf("build: %w\n%s", err, out)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	cmd := exec.Command(bin, "start", "--port="+strconv.Itoa(port), "--no-daemon")
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	stop := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(dir)
	}
	host := "localhost:" + strconv.Itoa(port)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if resp, err := http.Get("http://" + host + "/healthz"); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return host, stop, nil
			}
		}
	}
	stop()
	return "", nil, errors.New("emulator not ready after 10s")
}
