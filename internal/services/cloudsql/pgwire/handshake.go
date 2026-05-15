package pgwire

import (
	"context"
	"fmt"
	"io"

	"github.com/jackc/pgproto3/v2"
)

// handshake performs the initial Postgres startup. We refuse SSL (writing
// the single byte 'N' so the client retries without TLS), accept the
// StartupMessage with trust auth, advertise minimal parameters and a
// non-zero key for cancel requests (no-op), then send ReadyForQuery.
func (s *session) handshake(_ context.Context) error {
	for {
		msg, err := s.be.ReceiveStartupMessage()
		if err != nil {
			return fmt.Errorf("recv startup: %w", err)
		}
		switch msg.(type) {
		case *pgproto3.SSLRequest:
			if _, err := io.WriteString(asWriter(s.conn), "N"); err != nil {
				return err
			}
			// loop again for the real StartupMessage
		case *pgproto3.GSSEncRequest:
			if _, err := io.WriteString(asWriter(s.conn), "N"); err != nil {
				return err
			}
		case *pgproto3.CancelRequest:
			// no-op: nothing to cancel in this emulator
			return io.EOF
		case *pgproto3.StartupMessage:
			return s.completeStartup()
		default:
			return fmt.Errorf("unexpected startup message")
		}
	}
}

func (s *session) completeStartup() error {
	if err := s.be.Send(&pgproto3.AuthenticationOk{}); err != nil {
		return err
	}
	params := []pgproto3.ParameterStatus{
		{Name: "server_version", Value: "13.0 (gcp-local)"},
		{Name: "server_encoding", Value: "UTF8"},
		{Name: "client_encoding", Value: "UTF8"},
		{Name: "DateStyle", Value: "ISO, MDY"},
		{Name: "TimeZone", Value: "UTC"},
		{Name: "integer_datetimes", Value: "on"},
		{Name: "standard_conforming_strings", Value: "on"},
	}
	for _, p := range params {
		if err := s.be.Send(&p); err != nil {
			return err
		}
	}
	if err := s.be.Send(&pgproto3.BackendKeyData{ProcessID: 1, SecretKey: 1}); err != nil {
		return err
	}
	return s.be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
}

// asWriter is a tiny indirection so handshake.go doesn't need to know that
// session.conn is net.Conn (also an io.Writer). Helps tests later.
func asWriter(w io.Writer) io.Writer { return w }
