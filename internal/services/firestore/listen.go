package firestore

import (
	"errors"
	"strings"
	"sync"
	"time"

	pb "cloud.google.com/go/firestore/apiv1/firestorepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// listener is a single bidirectional Listen stream. Sends are serialised through
// a buffered outbox so the broadcast path never blocks on the network and so
// the gRPC stream's Send method is only ever called from one goroutine.
type listener struct {
	stream pb.Firestore_ListenServer
	outbox chan *pb.ListenResponse
	done   chan struct{}

	mu      sync.Mutex
	targets map[int32]listenTarget
}

// listenTarget is the resolved form of a single AddTarget request. We support
// the two shapes the SDK actually sends: an explicit list of document names
// (used by DocumentRef.Snapshots), and a single-collection query target (used
// by Query.Snapshots). Where/OrderBy filters on Query are ignored — the
// listener fires on any change in the collection, matching the coarse
// behaviour of RunQuery in this emulator.
type listenTarget struct {
	docNames   map[string]struct{}
	parentColl string // empty for document targets
}

func (t listenTarget) matches(docName string) bool {
	if _, ok := t.docNames[docName]; ok {
		return true
	}
	if t.parentColl == "" {
		return false
	}
	prefix := t.parentColl + "/"
	if !strings.HasPrefix(docName, prefix) {
		return false
	}
	return !strings.Contains(docName[len(prefix):], "/")
}

func (f *firestoreServer) Listen(stream pb.Firestore_ListenServer) error {
	l := &listener{
		stream:  stream,
		outbox:  make(chan *pb.ListenResponse, 64),
		done:    make(chan struct{}),
		targets: map[int32]listenTarget{},
	}

	f.svc.addListener(l)
	defer f.svc.removeListener(l)

	// Sender goroutine: drains outbox onto the stream. gRPC stream.Send is
	// not safe for concurrent calls, so all writes funnel through here.
	senderErr := make(chan error, 1)
	go func() {
		for {
			select {
			case <-l.done:
				senderErr <- nil
				return
			case msg := <-l.outbox:
				if err := stream.Send(msg); err != nil {
					senderErr <- err
					return
				}
			}
		}
	}()

	// Reader loop: stays in the main goroutine because gRPC streams have one
	// reader per stream. Each AddTarget snapshots current state then opens
	// the target up for live updates.
	for {
		req, err := stream.Recv()
		if err != nil {
			close(l.done)
			<-senderErr
			if errors.Is(err, errStreamEOF) || err.Error() == "EOF" {
				return nil
			}
			return err
		}
		switch tc := req.GetTargetChange().(type) {
		case *pb.ListenRequest_AddTarget:
			if err := f.addListenTarget(l, tc.AddTarget); err != nil {
				return err
			}
		case *pb.ListenRequest_RemoveTarget:
			l.mu.Lock()
			delete(l.targets, tc.RemoveTarget)
			l.mu.Unlock()
			l.send(&pb.ListenResponse{
				ResponseType: &pb.ListenResponse_TargetChange{
					TargetChange: &pb.TargetChange{
						TargetChangeType: pb.TargetChange_REMOVE,
						TargetIds:        []int32{tc.RemoveTarget},
					},
				},
			})
		}
	}
}

// errStreamEOF is a sentinel for the gRPC io.EOF case. We don't import io
// here to keep this file's deps lean; comparing on err string handles it.
var errStreamEOF = errors.New("EOF")

func (f *firestoreServer) addListenTarget(l *listener, target *pb.Target) error {
	if target == nil {
		return status.Error(codes.InvalidArgument, "target required")
	}
	targetID := target.GetTargetId()

	t := listenTarget{docNames: map[string]struct{}{}}
	switch tt := target.GetTargetType().(type) {
	case *pb.Target_Documents:
		for _, n := range tt.Documents.GetDocuments() {
			t.docNames[n] = struct{}{}
		}
	case *pb.Target_Query:
		parent := tt.Query.GetParent()
		sq := tt.Query.GetStructuredQuery()
		if sq == nil || len(sq.GetFrom()) == 0 {
			return status.Error(codes.InvalidArgument, "query target requires from")
		}
		t.parentColl = parent + "/" + sq.GetFrom()[0].GetCollectionId()
	default:
		return status.Error(codes.InvalidArgument, "unsupported target type")
	}

	l.mu.Lock()
	l.targets[targetID] = t
	l.mu.Unlock()

	// ADD acknowledgement before delivering initial state.
	l.send(&pb.ListenResponse{
		ResponseType: &pb.ListenResponse_TargetChange{
			TargetChange: &pb.TargetChange{
				TargetChangeType: pb.TargetChange_ADD,
				TargetIds:        []int32{targetID},
			},
		},
	})

	// Initial snapshot: emit DocumentChange for every doc that matches.
	if t.parentColl != "" {
		docs := f.svc.listColl(t.parentColl)
		for _, d := range docs {
			l.send(&pb.ListenResponse{
				ResponseType: &pb.ListenResponse_DocumentChange{
					DocumentChange: &pb.DocumentChange{
						Document:  toProtoDoc(d),
						TargetIds: []int32{targetID},
					},
				},
			})
		}
	} else {
		for name := range t.docNames {
			if d, err := f.svc.getDoc(name); err == nil {
				l.send(&pb.ListenResponse{
					ResponseType: &pb.ListenResponse_DocumentChange{
						DocumentChange: &pb.DocumentChange{
							Document:  toProtoDoc(d),
							TargetIds: []int32{targetID},
						},
					},
				})
			} else {
				l.send(&pb.ListenResponse{
					ResponseType: &pb.ListenResponse_DocumentDelete{
						DocumentDelete: &pb.DocumentDelete{
							Document:         name,
							RemovedTargetIds: []int32{targetID},
						},
					},
				})
			}
		}
	}

	// CURRENT marks "I've shown you the initial state". NO_CHANGE with a
	// read_time then closes the snapshot so the SDK delivers it to the
	// application callback.
	now := timestamppb.New(time.Now().UTC())
	l.send(&pb.ListenResponse{
		ResponseType: &pb.ListenResponse_TargetChange{
			TargetChange: &pb.TargetChange{
				TargetChangeType: pb.TargetChange_CURRENT,
				TargetIds:        []int32{targetID},
				ReadTime:         now,
			},
		},
	})
	l.send(&pb.ListenResponse{
		ResponseType: &pb.ListenResponse_TargetChange{
			TargetChange: &pb.TargetChange{
				TargetChangeType: pb.TargetChange_NO_CHANGE,
				ReadTime:         now,
			},
		},
	})
	return nil
}

// send drops to the sender goroutine. Returns immediately; a full outbox
// closes the listener since it implies the client is slower than mutations
// can be produced.
func (l *listener) send(msg *pb.ListenResponse) {
	select {
	case l.outbox <- msg:
	case <-l.done:
	default:
		// Outbox full — abandon this listener rather than block writers.
		select {
		case <-l.done:
		default:
			close(l.done)
		}
	}
}

func (s *Service) addListener(l *listener) {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	s.listeners = append(s.listeners, l)
}

func (s *Service) removeListener(l *listener) {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	for i, x := range s.listeners {
		if x == l {
			s.listeners = append(s.listeners[:i], s.listeners[i+1:]...)
			return
		}
	}
}

// sendNoChange follows up a change/delete with a NO_CHANGE marker so the
// SDK materialises the snapshot and delivers it to the application listener.
func (l *listener) sendNoChange() {
	l.send(&pb.ListenResponse{
		ResponseType: &pb.ListenResponse_TargetChange{
			TargetChange: &pb.TargetChange{
				TargetChangeType: pb.TargetChange_NO_CHANGE,
				ReadTime:         timestamppb.New(time.Now().UTC()),
			},
		},
	})
}

// broadcastChange is called by document mutations to fan out a DocumentChange.
func (s *Service) broadcastChange(d *storedDoc) {
	if d == nil {
		return
	}
	s.listenerMu.Lock()
	subs := append([]*listener{}, s.listeners...)
	s.listenerMu.Unlock()

	proto := toProtoDoc(d)
	for _, l := range subs {
		l.mu.Lock()
		var matched []int32
		for id, t := range l.targets {
			if t.matches(d.Name) {
				matched = append(matched, id)
			}
		}
		l.mu.Unlock()
		if len(matched) == 0 {
			continue
		}
		l.send(&pb.ListenResponse{
			ResponseType: &pb.ListenResponse_DocumentChange{
				DocumentChange: &pb.DocumentChange{
					Document:  proto,
					TargetIds: matched,
				},
			},
		})
		l.sendNoChange()
	}
}

// broadcastDelete fans out a DocumentDelete for the named doc.
func (s *Service) broadcastDelete(name string) {
	s.listenerMu.Lock()
	subs := append([]*listener{}, s.listeners...)
	s.listenerMu.Unlock()

	for _, l := range subs {
		l.mu.Lock()
		var matched []int32
		for id, t := range l.targets {
			if t.matches(name) {
				matched = append(matched, id)
			}
		}
		l.mu.Unlock()
		if len(matched) == 0 {
			continue
		}
		l.send(&pb.ListenResponse{
			ResponseType: &pb.ListenResponse_DocumentDelete{
				DocumentDelete: &pb.DocumentDelete{
					Document:         name,
					RemovedTargetIds: matched,
				},
			},
		})
		l.sendNoChange()
	}
}
