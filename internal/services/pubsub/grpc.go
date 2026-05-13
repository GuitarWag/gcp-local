package pubsub

import (
	"context"
	"errors"
	"io"
	"time"

	pb "cloud.google.com/go/pubsub/apiv1/pubsubpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// RegisterGRPC attaches the Publisher and Subscriber services to s grpc Server.
func (svc *Service) RegisterGRPC(g *grpc.Server) {
	pb.RegisterPublisherServer(g, &publisherServer{svc: svc})
	pb.RegisterSubscriberServer(g, &subscriberServer{svc: svc})
}

type publisherServer struct {
	pb.UnimplementedPublisherServer
	svc *Service
}

func (p *publisherServer) CreateTopic(_ context.Context, t *pb.Topic) (*pb.Topic, error) {
	if err := p.svc.CreateTopic(t.GetName()); err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			return nil, status.Error(codes.AlreadyExists, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.Topic{Name: t.GetName()}, nil
}

func (p *publisherServer) GetTopic(_ context.Context, req *pb.GetTopicRequest) (*pb.Topic, error) {
	ok, err := p.svc.GetTopic(req.GetTopic())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "topic not found")
	}
	return &pb.Topic{Name: req.GetTopic()}, nil
}

func (p *publisherServer) DeleteTopic(_ context.Context, req *pb.DeleteTopicRequest) (*emptypb.Empty, error) {
	if err := p.svc.DeleteTopic(req.GetTopic()); err != nil {
		return nil, status.Error(codes.NotFound, "topic not found")
	}
	return &emptypb.Empty{}, nil
}

func (p *publisherServer) Publish(_ context.Context, req *pb.PublishRequest) (*pb.PublishResponse, error) {
	msgs := make([]Message, 0, len(req.GetMessages()))
	for _, m := range req.GetMessages() {
		msgs = append(msgs, Message{
			Data:        m.GetData(),
			Attributes:  m.GetAttributes(),
			OrderingKey: m.GetOrderingKey(),
		})
	}
	ids, err := p.svc.PublishMessages(req.GetTopic(), msgs)
	if err != nil {
		if errors.Is(err, ErrTopicMissing) {
			return nil, status.Error(codes.NotFound, "topic not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pb.PublishResponse{MessageIds: ids}, nil
}

func (p *publisherServer) ListTopics(_ context.Context, req *pb.ListTopicsRequest) (*pb.ListTopicsResponse, error) {
	names, err := p.svc.ListTopics(req.GetProject() + "/topics/")
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &pb.ListTopicsResponse{}
	for _, n := range names {
		out.Topics = append(out.Topics, &pb.Topic{Name: n})
	}
	return out, nil
}

type subscriberServer struct {
	pb.UnimplementedSubscriberServer
	svc *Service
}

func (s *subscriberServer) CreateSubscription(_ context.Context, sub *pb.Subscription) (*pb.Subscription, error) {
	push := ""
	if pc := sub.GetPushConfig(); pc != nil {
		push = pc.GetPushEndpoint()
	}
	if err := s.svc.CreateSubscription(sub.GetName(), sub.GetTopic(), int(sub.GetAckDeadlineSeconds()), push); err != nil {
		if errors.Is(err, ErrTopicMissing) {
			return nil, status.Error(codes.NotFound, "topic not found")
		}
		if errors.Is(err, ErrAlreadyExists) {
			return nil, status.Error(codes.AlreadyExists, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	ack := sub.GetAckDeadlineSeconds()
	if ack == 0 {
		ack = 10
	}
	return &pb.Subscription{
		Name:               sub.GetName(),
		Topic:              sub.GetTopic(),
		AckDeadlineSeconds: ack,
		PushConfig:         sub.GetPushConfig(),
	}, nil
}

func (s *subscriberServer) GetSubscription(_ context.Context, req *pb.GetSubscriptionRequest) (*pb.Subscription, error) {
	topic, ack, err := s.svc.GetSubscription(req.GetSubscription())
	if err != nil {
		return nil, status.Error(codes.NotFound, "subscription not found")
	}
	return &pb.Subscription{
		Name:               req.GetSubscription(),
		Topic:              topic,
		AckDeadlineSeconds: int32(ack),
	}, nil
}

func (s *subscriberServer) DeleteSubscription(_ context.Context, req *pb.DeleteSubscriptionRequest) (*emptypb.Empty, error) {
	if err := s.svc.DeleteSubscription(req.GetSubscription()); err != nil {
		return nil, status.Error(codes.NotFound, "subscription not found")
	}
	return &emptypb.Empty{}, nil
}

func (s *subscriberServer) Pull(_ context.Context, req *pb.PullRequest) (*pb.PullResponse, error) {
	msgs, err := s.svc.PullMessages(req.GetSubscription(), int(req.GetMaxMessages()))
	if err != nil {
		return nil, status.Error(codes.NotFound, "subscription not found")
	}
	out := &pb.PullResponse{}
	for _, m := range msgs {
		out.ReceivedMessages = append(out.ReceivedMessages, &pb.ReceivedMessage{
			AckId: m.AckID,
			Message: &pb.PubsubMessage{
				MessageId:   m.Message.ID,
				Data:        m.Message.Data,
				Attributes:  m.Message.Attributes,
				OrderingKey: m.Message.OrderingKey,
			},
		})
	}
	return out, nil
}

func (s *subscriberServer) Acknowledge(_ context.Context, req *pb.AcknowledgeRequest) (*emptypb.Empty, error) {
	if err := s.svc.Acknowledge(req.GetSubscription(), req.GetAckIds()); err != nil {
		return nil, status.Error(codes.NotFound, "subscription not found")
	}
	return &emptypb.Empty{}, nil
}

func (s *subscriberServer) ModifyAckDeadline(_ context.Context, req *pb.ModifyAckDeadlineRequest) (*emptypb.Empty, error) {
	if err := s.svc.ModifyAckDeadline(req.GetSubscription(), req.GetAckIds(), int(req.GetAckDeadlineSeconds())); err != nil {
		return nil, status.Error(codes.NotFound, "subscription not found")
	}
	return &emptypb.Empty{}, nil
}

func (s *subscriberServer) StreamingPull(stream pb.Subscriber_StreamingPullServer) error {
	// First request carries the subscription name and initial config.
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	subName := first.GetSubscription()
	if subName == "" {
		return status.Error(codes.InvalidArgument, "subscription required")
	}
	if _, _, err := s.svc.GetSubscription(subName); err != nil {
		return status.Error(codes.NotFound, "subscription not found")
	}

	// Process any acks/modAcks in the first request and then run reader+pusher loops.
	if len(first.GetAckIds()) > 0 {
		_ = s.svc.Acknowledge(subName, first.GetAckIds())
	}

	ctx := stream.Context()
	recvErr := make(chan error, 1)
	go func() {
		for {
			req, err := stream.Recv()
			if err == io.EOF {
				recvErr <- nil
				return
			}
			if err != nil {
				recvErr <- err
				return
			}
			if ids := req.GetAckIds(); len(ids) > 0 {
				_ = s.svc.Acknowledge(subName, ids)
			}
			if mIDs := req.GetModifyDeadlineAckIds(); len(mIDs) > 0 {
				secs := req.GetModifyDeadlineSeconds()
				sec := 0
				if len(secs) > 0 {
					sec = int(secs[0])
				}
				_ = s.svc.ModifyAckDeadline(subName, mIDs, sec)
			}
		}
	}()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-recvErr:
			return err
		case <-ticker.C:
			msgs, err := s.svc.PullMessages(subName, 100)
			if err != nil {
				return status.Error(codes.Internal, err.Error())
			}
			if len(msgs) == 0 {
				continue
			}
			resp := &pb.StreamingPullResponse{}
			for _, m := range msgs {
				resp.ReceivedMessages = append(resp.ReceivedMessages, &pb.ReceivedMessage{
					AckId: m.AckID,
					Message: &pb.PubsubMessage{
						MessageId:   m.Message.ID,
						Data:        m.Message.Data,
						Attributes:  m.Message.Attributes,
						OrderingKey: m.Message.OrderingKey,
					},
				})
			}
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
	}
}
