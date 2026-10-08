use std::collections::HashSet;
use std::time::{SystemTime, UNIX_EPOCH};
use tonic::Status;
pub mod admission;
pub mod pb {
    tonic::include_proto!("jansetu.recommendation.v1");
}
use pb::{Candidate, RankedReference, RecommendRequest, RecommendResponse};
pub const POLICY: &str = "explicit-relevance-v1";
pub const MODEL: &str = "rules-v1";

pub fn score(c: &Candidate) -> f64 {
    0.35 * c.explicit_interest
        + 0.25 * c.locality
        + 0.20 * c.relationship
        + 0.10 * c.freshness
        + 0.10 * c.bounded_usefulness
}
fn explanation(c: &Candidate) -> &'static str {
    if c.explicit_interest > 0.0 {
        "EXPLICIT_INTEREST"
    } else if c.locality > 0.0 {
        "CHOSEN_LOCALITY"
    } else if c.relationship > 0.0 {
        "FOLLOWING"
    } else {
        "RECENT_PUBLIC_POST"
    }
}
pub fn rank(mut req: RecommendRequest) -> Result<RecommendResponse, Status> {
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as i64;
    if req.deadline_unix_ms <= now {
        return Err(Status::deadline_exceeded("ranking deadline"));
    }
    if req.surface != "HOME"
        || req.viewer_context.is_empty()
        || req.snapshot_id.is_empty()
        || req.candidates.len() > 2000
        || req.limit == 0
        || req.limit > 200
    {
        return Err(Status::invalid_argument(
            "invalid serving budget or context",
        ));
    }
    let mut ids = HashSet::new();
    for c in &req.candidates {
        if c.id.is_empty()
            || c.author_id.is_empty()
            || c.dedup_key.is_empty()
            || c.revision < 1
            || !ids.insert(c.id.clone())
            || [
                c.explicit_interest,
                c.locality,
                c.relationship,
                c.freshness,
                c.bounded_usefulness,
            ]
            .iter()
            .any(|v| !v.is_finite() || !(0.0..=1.0).contains(v))
        {
            return Err(Status::invalid_argument("invalid candidate"));
        }
    }
    req.candidates
        .sort_by(|a, b| score(b).total_cmp(&score(a)).then(a.id.cmp(&b.id)));
    let mut seen = HashSet::new();
    let mut conversations = HashSet::new();
    let items = req
        .candidates
        .into_iter()
        .filter(|c| {
            let conversation = if c.conversation_key.is_empty() {
                &c.id
            } else {
                &c.conversation_key
            };
            if seen.contains(&c.dedup_key) || conversations.contains(conversation) {
                return false;
            }
            seen.insert(c.dedup_key.clone());
            conversations.insert(conversation.clone());
            true
        })
        .take(req.limit as usize)
        .map(|c| RankedReference {
            explanation: explanation(&c).into(),
            id: c.id,
            revision: c.revision,
        })
        .collect();
    Ok(RecommendResponse {
        items,
        model_version: MODEL.into(),
        policy_version: POLICY.into(),
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    fn request(candidates: Vec<Candidate>) -> RecommendRequest {
        RecommendRequest {
            viewer_context: "request".into(),
            snapshot_id: "snapshot".into(),
            surface: "HOME".into(),
            deadline_unix_ms: i64::MAX,
            limit: 200,
            candidates,
            ..Default::default()
        }
    }
    fn candidate(id: &str) -> Candidate {
        Candidate {
            id: id.into(),
            revision: 1,
            author_id: "author".into(),
            dedup_key: id.into(),
            ..Default::default()
        }
    }
    #[test]
    fn documented_formula_ties_and_dedup() {
        let mut a = candidate("a");
        a.explicit_interest = 1.0;
        let mut b = candidate("b");
        b.locality = 1.0;
        b.freshness = 0.5;
        assert!((score(&a) - 0.35).abs() < 1e-10);
        let mut duplicate = a.clone();
        duplicate.id = "c".into();
        let result = rank(request(vec![b, a, duplicate])).unwrap();
        assert_eq!(
            result
                .items
                .iter()
                .map(|r| r.id.as_str())
                .collect::<Vec<_>>(),
            vec!["a", "b"]
        );
        assert_eq!(result.items[0].explanation, "EXPLICIT_INTEREST");
    }
    #[test]
    fn collapses_published_conversation_branches() {
        let mut root = candidate("root");
        root.conversation_key = "root".into();
        let mut quote = candidate("quote");
        quote.conversation_key = "root".into();
        let result = rank(request(vec![root, quote])).unwrap();
        assert_eq!(result.items.len(), 1);
    }
    #[test]
    fn rejects_invalid_features_budgets_and_deadlines() {
        for value in [f64::NAN, f64::INFINITY, -1.0, 1.1] {
            let mut c = candidate("a");
            c.freshness = value;
            assert!(rank(request(vec![c])).is_err());
        }
        let mut r = request(vec![]);
        r.limit = 201;
        assert!(rank(r).is_err());
        let mut r = request(vec![]);
        r.deadline_unix_ms = 0;
        assert_eq!(rank(r).unwrap_err().code(), tonic::Code::DeadlineExceeded);
        assert!(rank(request(vec![candidate("a"); 2001])).is_err());
    }
}
