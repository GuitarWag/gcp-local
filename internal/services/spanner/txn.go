package spanner

// txn.go: read-write transactions. DML under a transaction id is buffered,
// not applied: Commit replays the buffer plus the mutations atomically,
// Rollback drops it. Reads and DML inside the transaction see its earlier
// DML by replaying the buffer in a SQLite transaction that is rolled back.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type bufferedDML struct {
	sql  string
	args []any
}

type txnRecord struct {
	id      []byte
	session string
	pdml    bool          // partitioned DML: applies immediately, never committed
	dml     []bufferedDML // guarded by Service.mu
}

func (s *Service) BeginTransaction(ctx context.Context, req *sppb.BeginTransactionRequest) (*sppb.Transaction, error) {
	sess, err := s.lookupSession(req.GetSession())
	if err != nil {
		return nil, err
	}
	tx := s.beginTxn(sess, req.GetOptions())
	return &sppb.Transaction{Id: tx.id}, nil
}

func (s *Service) beginTxn(sess *sessionRecord, opts *sppb.TransactionOptions) *txnRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	id := []byte(fmt.Sprintf("txn-%s-%d", sess.name[strings.LastIndex(sess.name, "/")+1:], s.seq))
	rec := &txnRecord{id: id, session: sess.name, pdml: opts.GetPartitionedDml() != nil}
	s.txns[string(id)] = rec
	return rec
}

// resolveTxn maps a TransactionSelector to a transaction record. begun is
// true when the selector started a new transaction inline (Begin), whose id
// the response must carry. A nil record means autocommit (single-use).
func (s *Service) resolveTxn(sess *sessionRecord, sel *sppb.TransactionSelector) (rec *txnRecord, begun bool, err error) {
	switch {
	case sel.GetBegin() != nil:
		return s.beginTxn(sess, sel.GetBegin()), true, nil
	case sel.GetId() != nil:
		s.mu.Lock()
		defer s.mu.Unlock()
		rec, ok := s.txns[string(sel.GetId())]
		if !ok {
			return nil, false, errNotFound("Transaction not found: %s", sel.GetId())
		}
		return rec, false, nil
	}
	return nil, false, nil
}

func (s *Service) dropTxn(id []byte) *txnRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.txns[string(id)]
	delete(s.txns, string(id))
	return rec
}

// runInTxn executes one statement on top of the transaction's buffered DML
// and rolls the SQLite transaction back. DML that succeeds joins the buffer.
// ponytail: replays the whole buffer per statement (O(n²)); fine for tests.
func (s *Service) runInTxn(db *sql.DB, txn *txnRecord, kind, q string, args []any) (*sppb.ResultSet, error) {
	s.mu.Lock()
	buffered := append([]bufferedDML(nil), txn.dml...)
	s.mu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return nil, errInternal("begin sqlite tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := replayDML(tx, buffered); err != nil {
		return nil, err
	}
	rs, err := runStmt(tx, kind, q, args)
	if err != nil {
		return nil, err
	}
	if kind == "dml" {
		s.mu.Lock()
		txn.dml = append(txn.dml, bufferedDML{sql: q, args: args})
		s.mu.Unlock()
	}
	return rs, nil
}

// withTxnID attaches an inline-begun transaction id to a result set.
func withTxnID(rs *sppb.ResultSet, txn *txnRecord) {
	if rs.Metadata == nil {
		rs.Metadata = &sppb.ResultSetMetadata{}
	}
	rs.Metadata.Transaction = &sppb.Transaction{Id: txn.id}
}

func (s *Service) Commit(ctx context.Context, req *sppb.CommitRequest) (*sppb.CommitResponse, error) {
	sess, err := s.lookupSession(req.GetSession())
	if err != nil {
		return nil, err
	}
	var dml []bufferedDML
	if id := req.GetTransactionId(); id != nil {
		txn := s.dropTxn(id)
		if txn == nil {
			return nil, errNotFound("Transaction not found: %s", id)
		}
		s.mu.Lock()
		dml = txn.dml
		s.mu.Unlock()
	}
	if err := s.commitWrites(sess.database, dml, req.GetMutations()); err != nil {
		return nil, err
	}
	return &sppb.CommitResponse{CommitTimestamp: timestamppb.New(time.Now().UTC())}, nil
}

func (s *Service) Rollback(ctx context.Context, req *sppb.RollbackRequest) (*emptypb.Empty, error) {
	// Unknown transactions are tolerated, matching the real service.
	s.dropTxn(req.GetTransactionId())
	return &emptypb.Empty{}, nil
}

// ExecuteBatchDml runs DML statements in order, stopping at the first error
// (per the Spanner contract) and reporting per-statement counts. Under a
// transaction the statements join its buffer, so Rollback discards them.
func (s *Service) ExecuteBatchDml(ctx context.Context, req *sppb.ExecuteBatchDmlRequest) (*sppb.ExecuteBatchDmlResponse, error) {
	sess, err := s.lookupSession(req.GetSession())
	if err != nil {
		return nil, err
	}
	txn, begun, err := s.resolveTxn(sess, req.GetTransaction())
	if err != nil {
		return nil, err
	}
	resp := &sppb.ExecuteBatchDmlResponse{}
	for _, st := range req.GetStatements() {
		rs, err := s.runSQL(sess, st.GetSql(), st.GetParams(), st.GetParamTypes(), txn)
		if err != nil {
			resp.Status = status.Convert(err).Proto()
			break
		}
		resp.ResultSets = append(resp.ResultSets, rs)
	}
	if begun {
		if len(resp.ResultSets) == 0 {
			s.dropTxn(txn.id)
		} else {
			withTxnID(resp.ResultSets[0], txn)
		}
	}
	return resp, nil
}
