// Package recommendation exposes a narrow serving boundary; authority remains in Go.
package recommendation

import (
	"context"
	"errors"

	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Ranker interface {
	Recommend(context.Context, *pb.RecommendRequest) (*pb.RecommendResponse, error)
}
type Client struct {
	conn *grpc.ClientConn
	rpc  pb.RecommendationServiceClient
}

func New(target string) (*Client, error) {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(128*1024), grpc.MaxCallSendMsgSize(1024*1024)))
	if err != nil {
		return nil, err
	}
	return &Client{conn, pb.NewRecommendationServiceClient(conn)}, nil
}
func (c *Client) Close() error { return c.conn.Close() }
func (c *Client) Recommend(ctx context.Context, r *pb.RecommendRequest) (*pb.RecommendResponse, error) {
	result, err := c.rpc.Recommend(ctx, r)
	if err != nil {
		return nil, err
	}
	if err = Validate(r, result); err != nil {
		return nil, err
	}
	return result, nil
}

// A service result is never trusted to introduce content or change its revision.
func Validate(r *pb.RecommendRequest, result *pb.RecommendResponse) error {
	if result == nil || len(result.Items) > int(r.Limit) || result.ModelVersion == "" || result.PolicyVersion == "" || len(result.ModelVersion) > 80 || len(result.PolicyVersion) > 80 {
		return errors.New("invalid ranking result")
	}
	candidates := map[string]*pb.Candidate{}
	for _, c := range r.Candidates {
		candidates[c.Id] = c
	}
	seen := map[string]bool{}
	bodies := map[string]bool{}
	threads := map[string]bool{}
	for _, item := range result.Items {
		if item == nil || seen[item.Id] || candidates[item.Id] == nil || candidates[item.Id].Revision != item.Revision || item.Revision < 1 {
			return errors.New("ranking returned unknown or duplicate revision")
		}
		c := candidates[item.Id]
		if (c.DedupKey != "" && bodies[c.DedupKey]) || (c.ConversationKey != "" && threads[c.ConversationKey]) {
			return errors.New("duplicate ranked content")
		}
		if (item.Explanation == "EXPLICIT_INTEREST" && c.ExplicitInterest <= 0) || (item.Explanation == "CHOSEN_LOCALITY" && c.Locality <= 0) || (item.Explanation == "FOLLOWING" && c.Relationship <= 0) {
			return errors.New("unsupported explanation")
		}
		bodies[c.DedupKey] = true
		threads[c.ConversationKey] = true
		switch item.Explanation {
		case "EXPLICIT_INTEREST", "CHOSEN_LOCALITY", "FOLLOWING", "RECENT_PUBLIC_POST":
		default:
			return errors.New("invalid explanation code")
		}
		seen[item.Id] = true
	}
	return nil
}
