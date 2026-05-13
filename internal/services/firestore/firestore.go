package firestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pb "cloud.google.com/go/firestore/apiv1/firestorepb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/state"
)

const nsDocs = "firestore/documents"

type storedDoc struct {
	Name       string                     `json:"name"`
	Fields     map[string]json.RawMessage `json:"fields"`
	CreateTime time.Time                  `json:"createTime"`
	UpdateTime time.Time                  `json:"updateTime"`
}

type Service struct {
	store   state.Store
	project string
	mu      sync.Mutex
	verSeq  uint64

	listenerMu sync.Mutex
	listeners  []*listener
}

func New(store state.Store, cfg *config.Config) (*Service, error) {
	return &Service{store: store, project: cfg.Project}, nil
}

func (s *Service) Name() string              { return "firestore" }
func (s *Service) Register(_ *http.ServeMux) {}

func (s *Service) RegisterGRPC(g *grpc.Server) {
	pb.RegisterFirestoreServer(g, &firestoreServer{svc: s})
}

func (s *Service) getDoc(name string) (*storedDoc, error) {
	data, err := s.store.Get(nsDocs, name)
	if err != nil {
		return nil, err
	}
	var d storedDoc
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *Service) putDoc(d *storedDoc) error {
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return s.store.Put(nsDocs, d.Name, data)
}

func (s *Service) listColl(parent string) []*storedDoc {
	prefix := parent + "/"
	all, _ := s.store.List(nsDocs, prefix)
	out := []*storedDoc{}
	for k, v := range all {
		// Only direct children (no nested subcollections under this doc path).
		suffix := strings.TrimPrefix(k, prefix)
		if strings.Contains(suffix, "/") {
			continue
		}
		var d storedDoc
		if json.Unmarshal(v, &d) == nil {
			out = append(out, &d)
		}
	}
	return out
}

type firestoreServer struct {
	pb.UnimplementedFirestoreServer
	svc *Service
}

func (f *firestoreServer) GetDocument(_ context.Context, req *pb.GetDocumentRequest) (*pb.Document, error) {
	d, err := f.svc.getDoc(req.GetName())
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "document not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return toProtoDoc(d), nil
}

func (f *firestoreServer) CreateDocument(_ context.Context, req *pb.CreateDocumentRequest) (*pb.Document, error) {
	name := req.GetParent() + "/" + req.GetCollectionId() + "/" + req.GetDocumentId()
	f.svc.mu.Lock()
	defer f.svc.mu.Unlock()
	if _, err := f.svc.getDoc(name); err == nil {
		return nil, status.Error(codes.AlreadyExists, "document exists")
	}
	now := time.Now().UTC()
	fields, err := marshalFields(req.GetDocument().GetFields())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	d := &storedDoc{Name: name, Fields: fields, CreateTime: now, UpdateTime: now}
	if err := f.svc.putDoc(d); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	f.svc.broadcastChange(d)
	return toProtoDoc(d), nil
}

func (f *firestoreServer) UpdateDocument(_ context.Context, req *pb.UpdateDocumentRequest) (*pb.Document, error) {
	name := req.GetDocument().GetName()
	now := time.Now().UTC()
	fields, err := marshalFields(req.GetDocument().GetFields())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	f.svc.mu.Lock()
	defer f.svc.mu.Unlock()
	existing, err := f.svc.getDoc(name)
	var d *storedDoc
	if err == nil {
		d = existing
		// If updateMask is set, merge; otherwise replace.
		if mask := req.GetUpdateMask(); mask != nil && len(mask.GetFieldPaths()) > 0 {
			if d.Fields == nil {
				d.Fields = map[string]json.RawMessage{}
			}
			for k, v := range fields {
				d.Fields[k] = v
			}
		} else {
			d.Fields = fields
		}
		d.UpdateTime = now
	} else {
		d = &storedDoc{Name: name, Fields: fields, CreateTime: now, UpdateTime: now}
	}
	if err := f.svc.putDoc(d); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	f.svc.broadcastChange(d)
	return toProtoDoc(d), nil
}

func (f *firestoreServer) DeleteDocument(_ context.Context, req *pb.DeleteDocumentRequest) (*emptypb.Empty, error) {
	if err := f.svc.store.Delete(nsDocs, req.GetName()); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "document not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	f.svc.broadcastDelete(req.GetName())
	return &emptypb.Empty{}, nil
}

func (f *firestoreServer) ListDocuments(_ context.Context, req *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error) {
	parent := req.GetParent() + "/" + req.GetCollectionId()
	docs := f.svc.listColl(parent)
	out := &pb.ListDocumentsResponse{}
	for _, d := range docs {
		out.Documents = append(out.Documents, toProtoDoc(d))
	}
	return out, nil
}

func (f *firestoreServer) Commit(_ context.Context, req *pb.CommitRequest) (*pb.CommitResponse, error) {
	now := time.Now().UTC()
	writeResults := []*pb.WriteResult{}
	atomic.AddUint64(&f.svc.verSeq, 1)
	// Collect mutations under the lock; broadcast after we release it so
	// listener fan-out can't deadlock on a listener that's slow to drain.
	var changes []*storedDoc
	var deletes []string
	f.svc.mu.Lock()
	for _, wr := range req.GetWrites() {
		switch op := wr.GetOperation().(type) {
		case *pb.Write_Update:
			doc := op.Update
			name := doc.GetName()
			fields, err := marshalFields(doc.GetFields())
			if err != nil {
				f.svc.mu.Unlock()
				return nil, status.Error(codes.Internal, err.Error())
			}
			existing, _ := f.svc.getDoc(name)
			var d *storedDoc
			if existing != nil {
				d = existing
				if mask := wr.GetUpdateMask(); mask != nil && len(mask.GetFieldPaths()) > 0 {
					if d.Fields == nil {
						d.Fields = map[string]json.RawMessage{}
					}
					for _, fp := range mask.GetFieldPaths() {
						if v, ok := fields[fp]; ok {
							d.Fields[fp] = v
						} else {
							delete(d.Fields, fp)
						}
					}
				} else {
					d.Fields = fields
				}
				d.UpdateTime = now
			} else {
				d = &storedDoc{Name: name, Fields: fields, CreateTime: now, UpdateTime: now}
			}
			if err := f.svc.putDoc(d); err != nil {
				f.svc.mu.Unlock()
				return nil, status.Error(codes.Internal, err.Error())
			}
			changes = append(changes, d)
			writeResults = append(writeResults, &pb.WriteResult{UpdateTime: timestamppb.New(d.UpdateTime)})
		case *pb.Write_Delete:
			if err := f.svc.store.Delete(nsDocs, op.Delete); err == nil {
				deletes = append(deletes, op.Delete)
			}
			writeResults = append(writeResults, &pb.WriteResult{UpdateTime: timestamppb.New(now)})
		default:
			writeResults = append(writeResults, &pb.WriteResult{UpdateTime: timestamppb.New(now)})
		}
	}
	f.svc.mu.Unlock()
	for _, d := range changes {
		f.svc.broadcastChange(d)
	}
	for _, name := range deletes {
		f.svc.broadcastDelete(name)
	}
	return &pb.CommitResponse{
		WriteResults: writeResults,
		CommitTime:   timestamppb.New(now),
	}, nil
}

func (f *firestoreServer) BatchWrite(_ context.Context, req *pb.BatchWriteRequest) (*pb.BatchWriteResponse, error) {
	commit := &pb.CommitRequest{Database: req.GetDatabase(), Writes: req.GetWrites()}
	resp, err := f.Commit(context.Background(), commit)
	if err != nil {
		return nil, err
	}
	out := &pb.BatchWriteResponse{}
	for _, wr := range resp.GetWriteResults() {
		out.WriteResults = append(out.WriteResults, wr)
		out.Status = append(out.Status, nil)
	}
	return out, nil
}

func (f *firestoreServer) RunQuery(req *pb.RunQueryRequest, stream pb.Firestore_RunQueryServer) error {
	parent := req.GetParent()
	collID := ""
	if sq := req.GetStructuredQuery(); sq != nil && len(sq.GetFrom()) > 0 {
		collID = sq.GetFrom()[0].GetCollectionId()
	}
	if collID == "" {
		return status.Error(codes.InvalidArgument, "collection id required")
	}
	parentColl := parent + "/" + collID
	docs := f.svc.listColl(parentColl)
	now := timestamppb.New(time.Now().UTC())
	for _, d := range docs {
		if err := stream.Send(&pb.RunQueryResponse{
			Document: toProtoDoc(d),
			ReadTime: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (f *firestoreServer) BatchGetDocuments(req *pb.BatchGetDocumentsRequest, stream pb.Firestore_BatchGetDocumentsServer) error {
	now := timestamppb.New(time.Now().UTC())
	for _, name := range req.GetDocuments() {
		d, err := f.svc.getDoc(name)
		if err != nil {
			if err := stream.Send(&pb.BatchGetDocumentsResponse{
				Result:   &pb.BatchGetDocumentsResponse_Missing{Missing: name},
				ReadTime: now,
			}); err != nil {
				return err
			}
			continue
		}
		if err := stream.Send(&pb.BatchGetDocumentsResponse{
			Result:   &pb.BatchGetDocumentsResponse_Found{Found: toProtoDoc(d)},
			ReadTime: now,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (f *firestoreServer) BeginTransaction(_ context.Context, _ *pb.BeginTransactionRequest) (*pb.BeginTransactionResponse, error) {
	return &pb.BeginTransactionResponse{Transaction: []byte(fmt.Sprintf("txn-%d", time.Now().UnixNano()))}, nil
}

func (f *firestoreServer) Rollback(_ context.Context, _ *pb.RollbackRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

// ---- helpers ----

func toProtoDoc(d *storedDoc) *pb.Document {
	doc := &pb.Document{
		Name:       d.Name,
		Fields:     map[string]*pb.Value{},
		CreateTime: timestamppb.New(d.CreateTime),
		UpdateTime: timestamppb.New(d.UpdateTime),
	}
	for k, v := range d.Fields {
		val, err := unmarshalValue(v)
		if err == nil {
			doc.Fields[k] = val
		}
	}
	return doc
}

func marshalFields(in map[string]*pb.Value) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for k, v := range in {
		raw, err := marshalValue(v)
		if err != nil {
			return nil, err
		}
		out[k] = raw
	}
	return out, nil
}

// We serialise proto Value as JSON of a tagged union for storage.
type storedValue struct {
	Kind      string                 `json:"kind"`
	Str       string                 `json:"str,omitempty"`
	Int       int64                  `json:"int,omitempty"`
	Double    float64                `json:"double,omitempty"`
	Bool      bool                   `json:"bool,omitempty"`
	BytesB64  string                 `json:"bytes,omitempty"`
	Timestamp time.Time              `json:"ts,omitempty"`
	Map       map[string]storedValue `json:"map,omitempty"`
	Array     []storedValue          `json:"array,omitempty"`
}

func marshalValue(v *pb.Value) (json.RawMessage, error) {
	sv := storedValueFromProto(v)
	return json.Marshal(sv)
}

func unmarshalValue(raw json.RawMessage) (*pb.Value, error) {
	var sv storedValue
	if err := json.Unmarshal(raw, &sv); err != nil {
		return nil, err
	}
	return protoFromStoredValue(sv), nil
}

func storedValueFromProto(v *pb.Value) storedValue {
	if v == nil {
		return storedValue{Kind: "null"}
	}
	switch t := v.GetValueType().(type) {
	case *pb.Value_NullValue:
		return storedValue{Kind: "null"}
	case *pb.Value_BooleanValue:
		return storedValue{Kind: "bool", Bool: t.BooleanValue}
	case *pb.Value_IntegerValue:
		return storedValue{Kind: "int", Int: t.IntegerValue}
	case *pb.Value_DoubleValue:
		return storedValue{Kind: "double", Double: t.DoubleValue}
	case *pb.Value_StringValue:
		return storedValue{Kind: "str", Str: t.StringValue}
	case *pb.Value_BytesValue:
		return storedValue{Kind: "bytes", BytesB64: string(t.BytesValue)}
	case *pb.Value_TimestampValue:
		return storedValue{Kind: "ts", Timestamp: t.TimestampValue.AsTime()}
	case *pb.Value_MapValue:
		out := storedValue{Kind: "map", Map: map[string]storedValue{}}
		for k, vv := range t.MapValue.GetFields() {
			out.Map[k] = storedValueFromProto(vv)
		}
		return out
	case *pb.Value_ArrayValue:
		out := storedValue{Kind: "array"}
		for _, vv := range t.ArrayValue.GetValues() {
			out.Array = append(out.Array, storedValueFromProto(vv))
		}
		return out
	}
	return storedValue{Kind: "null"}
}

func protoFromStoredValue(sv storedValue) *pb.Value {
	switch sv.Kind {
	case "null":
		return &pb.Value{ValueType: &pb.Value_NullValue{}}
	case "bool":
		return &pb.Value{ValueType: &pb.Value_BooleanValue{BooleanValue: sv.Bool}}
	case "int":
		return &pb.Value{ValueType: &pb.Value_IntegerValue{IntegerValue: sv.Int}}
	case "double":
		return &pb.Value{ValueType: &pb.Value_DoubleValue{DoubleValue: sv.Double}}
	case "str":
		return &pb.Value{ValueType: &pb.Value_StringValue{StringValue: sv.Str}}
	case "bytes":
		return &pb.Value{ValueType: &pb.Value_BytesValue{BytesValue: []byte(sv.BytesB64)}}
	case "ts":
		return &pb.Value{ValueType: &pb.Value_TimestampValue{TimestampValue: timestamppb.New(sv.Timestamp)}}
	case "map":
		m := &pb.MapValue{Fields: map[string]*pb.Value{}}
		for k, vv := range sv.Map {
			m.Fields[k] = protoFromStoredValue(vv)
		}
		return &pb.Value{ValueType: &pb.Value_MapValue{MapValue: m}}
	case "array":
		a := &pb.ArrayValue{}
		for _, vv := range sv.Array {
			a.Values = append(a.Values, protoFromStoredValue(vv))
		}
		return &pb.Value{ValueType: &pb.Value_ArrayValue{ArrayValue: a}}
	}
	return &pb.Value{ValueType: &pb.Value_NullValue{}}
}
