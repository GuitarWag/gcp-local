package spanner

// Package spanner implements the google.spanner.v1 data path plus the
// google.spanner.database.v1 admin surface needed by real client libraries:
// sessions, ExecuteSql/ExecuteStreamingSql (SELECT + DML), Read/StreamingRead
// (key sets), Commit/Rollback, and CreateDatabase / UpdateDatabaseDdl as
// already-complete LROs. State lives in one in-memory SQLite database per
// Spanner database (see engine.go).

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	adminpb "cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/GuitarWag/gcp-local/internal/config"
)

// sessionTTL matches the emulator's session lifetime.
const sessionTTL = time.Hour

// Service is the Spanner service: a real gRPC data path backed by SQLite.
type Service struct {
	project string
	engine  *engine

	mu       sync.Mutex
	sessions map[string]*sessionRecord
	txns     map[string]*txnRecord
	seq      uint64
}

type sessionRecord struct {
	name        string
	database    string
	multiplexed bool
	created     time.Time
	lastUsed    time.Time
	pendingTxn  []byte // txn begun via ExecuteSql(Begin), committed by Commit
}

type txnRecord struct {
	id      []byte
	session string
	begun   time.Time
}

func New(_ any, cfg *config.Config) (*Service, error) {
	return &Service{
		project:  cfg.Project,
		engine:   newEngine(),
		sessions: make(map[string]*sessionRecord),
		txns:     make(map[string]*txnRecord),
	}, nil
}

func (s *Service) Name() string              { return "spanner" }
func (s *Service) Register(_ *http.ServeMux) {}

func (s *Service) RegisterGRPC(g *grpc.Server) {
	sppb.RegisterSpannerServer(g, s)
	adminpb.RegisterDatabaseAdminServer(g, &adminServer{svc: s})
}

// ---------------------------------------------------------------------------
// Session lifecycle
// ---------------------------------------------------------------------------

func (s *Service) CreateSession(ctx context.Context, req *sppb.CreateSessionRequest) (*sppb.Session, error) {
	return s.newSession(req.GetDatabase(), req.GetSession().GetMultiplexed())
}

func (s *Service) BatchCreateSessions(ctx context.Context, req *sppb.BatchCreateSessionsRequest) (*sppb.BatchCreateSessionsResponse, error) {
	resp := &sppb.BatchCreateSessionsResponse{}
	for i := 0; i < int(req.GetSessionCount()); i++ {
		sess, err := s.newSession(req.GetDatabase(), false)
		if err != nil {
			return nil, err
		}
		resp.Session = append(resp.Session, sess)
	}
	return resp, nil
}

func (s *Service) GetSession(ctx context.Context, req *sppb.GetSessionRequest) (*sppb.Session, error) {
	sess, err := s.lookupSession(req.GetName())
	if err != nil {
		return nil, err
	}
	return sess.toProto(), nil
}

func (s *Service) ListSessions(ctx context.Context, req *sppb.ListSessionsRequest) (*sppb.ListSessionsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resp := &sppb.ListSessionsResponse{}
	for name, sess := range s.sessions {
		if strings.HasPrefix(name, req.GetDatabase()+"/sessions/") {
			resp.Sessions = append(resp.Sessions, sess.toProto())
		}
	}
	return resp, nil
}

func (s *Service) DeleteSession(ctx context.Context, req *sppb.DeleteSessionRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[req.GetName()]; !ok {
		return nil, errNotFound("Session with id %s not found", req.GetName())
	}
	delete(s.sessions, req.GetName())
	return &emptypb.Empty{}, nil
}

func (s *Service) newSession(database string, multiplexed bool) (*sppb.Session, error) {
	if database == "" {
		return nil, errInvalidArgument("missing database in session request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	name := fmt.Sprintf("%s/sessions/sess-%d", database, s.seq)
	now := time.Now().UTC()
	s.sessions[name] = &sessionRecord{
		name:        name,
		database:    database,
		multiplexed: multiplexed,
		created:     now,
		lastUsed:    now,
	}
	return &sppb.Session{
		Name:        name,
		CreateTime:  timestamppb.New(now),
		Multiplexed: multiplexed,
	}, nil
}

func (s *sessionRecord) toProto() *sppb.Session {
	return &sppb.Session{
		Name:                   s.name,
		CreateTime:             timestamppb.New(s.created),
		ApproximateLastUseTime: timestamppb.New(s.lastUsed),
		Multiplexed:            s.multiplexed,
	}
}

// lookupSession finds a live session and refreshes its last-use timestamp.
func (s *Service) lookupSession(name string) (*sessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[name]
	if !ok {
		return nil, errNotFound("Session with id %s not found", name)
	}
	if time.Since(sess.created) > sessionTTL {
		delete(s.sessions, name)
		return nil, errNotFound("Session with id %s not found", name)
	}
	sess.lastUsed = time.Now().UTC()
	return sess, nil
}

// ---------------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------------

func (s *Service) BeginTransaction(ctx context.Context, req *sppb.BeginTransactionRequest) (*sppb.Transaction, error) {
	sess, err := s.lookupSession(req.GetSession())
	if err != nil {
		return nil, err
	}
	tx, err := s.beginTxn(sess)
	if err != nil {
		return nil, err
	}
	return &sppb.Transaction{Id: tx.id}, nil
}

func (s *Service) beginTxn(sess *sessionRecord) (*txnRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := []byte(fmt.Sprintf("txn-%s-%d", sess.name[strings.LastIndex(sess.name, "/")+1:], s.seq))
	rec := &txnRecord{id: id, session: sess.name, begun: time.Now().UTC()}
	s.txns[string(id)] = rec
	return rec, nil
}

func (s *Service) Commit(ctx context.Context, req *sppb.CommitRequest) (*sppb.CommitResponse, error) {
	sess, err := s.lookupSession(req.GetSession())
	if err != nil {
		return nil, err
	}
	var txnID []byte
	mutations := req.GetMutations()
	switch t := req.GetTransaction().(type) {
	case *sppb.CommitRequest_TransactionId:
		txnID = t.TransactionId
	case *sppb.CommitRequest_SingleUseTransaction:
		// single-use implicit transaction: mutations apply directly
	default:
		// no selector: mutations-only commit on the session
	}
	if txnID == nil {
		// Fall back to a transaction begun by ExecuteSql(Begin).
		txnID = sess.pendingTxn
	}
	if txnID != nil {
		s.mu.Lock()
		_, ok := s.txns[string(txnID)]
		if !ok {
			s.mu.Unlock()
			return nil, errNotFound("Transaction with id %s not found", string(txnID))
		}
		delete(s.txns, string(txnID))
		s.mu.Unlock()
		sess.pendingTxn = nil
	}
	if len(mutations) > 0 {
		if err := s.applyMutations(sess.database, mutations); err != nil {
			return nil, err
		}
	}
	return &sppb.CommitResponse{CommitTimestamp: timestamppb.New(time.Now().UTC())}, nil
}

func (s *Service) Rollback(ctx context.Context, req *sppb.RollbackRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Unknown transactions are tolerated, matching the real service.
	delete(s.txns, string(req.GetTransactionId()))
	return &emptypb.Empty{}, nil
}

// PartitionQuery / PartitionRead are not supported (query parallelism is a
// documented non-goal); they return Unimplemented via the embedded type.
func (s *Service) PartitionQuery(ctx context.Context, req *sppb.PartitionQueryRequest) (*sppb.PartitionResponse, error) {
	return nil, errUnimplemented("PartitionQuery is not supported by the emulator")
}

func (s *Service) PartitionRead(ctx context.Context, req *sppb.PartitionReadRequest) (*sppb.PartitionResponse, error) {
	return nil, errUnimplemented("PartitionRead is not supported by the emulator")
}

// BatchWrite streams mutations; a small synchronous stand-in that applies
// each batch and reports commit timestamps (the SDK's legacy path uses
// Commit; BatchWrite is only exercised by newer clients).
func (s *Service) BatchWrite(req *sppb.BatchWriteRequest, stream sppb.Spanner_BatchWriteServer) error {
	sess, err := s.lookupSession(req.GetSession())
	if err != nil {
		return err
	}
	for _, batch := range req.GetMutationGroups() {
		if err := s.applyMutations(sess.database, batch.GetMutations()); err != nil {
			return err
		}
		if err := stream.Send(&sppb.BatchWriteResponse{
			CommitTimestamp: timestamppb.New(time.Now().UTC()),
			Indexes:         []int32{0},
		}); err != nil {
			return err
		}
	}
	return nil
}

// ExecuteBatchDml runs a batch of DML statements in order, stopping at the
// first error (per the Spanner contract) and reporting per-statement counts.
func (s *Service) ExecuteBatchDml(ctx context.Context, req *sppb.ExecuteBatchDmlRequest) (*sppb.ExecuteBatchDmlResponse, error) {
	sess, err := s.lookupSession(req.GetSession())
	if err != nil {
		return nil, err
	}
	resp := &sppb.ExecuteBatchDmlResponse{}
	for _, st := range req.GetStatements() {
		rs, err := s.runSQL(sess, st.GetSql(), st.GetParams(), st.GetParamTypes())
		if err != nil {
			st, _ := status.FromError(err)
			resp.Status = st.Proto()
			break
		}
		resp.ResultSets = append(resp.ResultSets, rs)
	}
	return resp, nil
}
