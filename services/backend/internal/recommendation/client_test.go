package recommendation

import (
	"github.com/lavkushry/JanSetu-AI/services/backend/internal/recommendation/pb"
	"testing"
)

func TestRejectsUntrustedReferenceResponses(t *testing.T) {
	request := &pb.RecommendRequest{Limit: 1, Candidates: []*pb.Candidate{{Id: "approved", Revision: 2}}}
	valid := &pb.RecommendResponse{ModelVersion: "rules-v1", PolicyVersion: "explicit-relevance-v1", Items: []*pb.RankedReference{{Id: "approved", Revision: 2, Explanation: "RECENT_PUBLIC_POST"}}}
	if err := Validate(request, valid); err != nil {
		t.Fatal(err)
	}
	for _, r := range []*pb.RecommendResponse{nil, {}, {ModelVersion: "rules-v1", PolicyVersion: "policy", Items: []*pb.RankedReference{{Id: "private", Revision: 2, Explanation: "RECENT_PUBLIC_POST"}}}, {ModelVersion: "rules-v1", PolicyVersion: "policy", Items: []*pb.RankedReference{{Id: "approved", Revision: 1, Explanation: "RECENT_PUBLIC_POST"}}}, {ModelVersion: "rules-v1", PolicyVersion: "policy", Items: []*pb.RankedReference{{Id: "approved", Revision: 2, Explanation: "PRIVATE_REPORT"}}}, {ModelVersion: "rules-v1", PolicyVersion: "policy", Items: []*pb.RankedReference{nil}}} {
		if Validate(request, r) == nil {
			t.Fatal("invalid response trusted", r)
		}
	}
	request.Limit = 2
	valid.Items = append(valid.Items, valid.Items[0])
	if Validate(request, valid) == nil {
		t.Fatal("duplicate response trusted")
	}
}
