# Reference inventory and reuse

Checked 2026-10-07 against the publishers' primary sources. These are design references, not a claim to know their full production systems or to reproduce their objectives.

| Reference | Pinned source | Pattern adopted | License/reuse |
| --- | --- | --- | --- |
| Original Twitter algorithm | [c54bec0d4e029fe34926ef3258a86ccacc0d0182](https://github.com/twitter/the-algorithm/tree/c54bec0d4e029fe34926ef3258a86ccacc0d0182) | Multiple candidate sources, composable stages, visibility enforcement | AGPL-3.0; no source, weights or assets copied |
| New X algorithm | [78460ca8b65c57ddd3a05f9217c8aaeba214b628](https://github.com/xai-org/x-algorithm/tree/78460ca8b65c57ddd3a05f9217c8aaeba214b628) | Retrieval/scoring/filter separation and multiple outcomes | Apache-2.0; no source, weights or assets copied |
| Instagram Explore | [Meta Engineering, 2023-08-09](https://engineering.fb.com/2023/08/09/ml-applications/scaling-instagram-explore-recommendations-system/) | Retrieval, inexpensive ranking, heavier ranking, final diversity | Reference only; no copied material |
| TikTok explanation | [TikTok Newsroom, 2020-06-18](https://newsroom.tiktok.com/how-tiktok-recommends-videos-for-you?lang=en) | Changing interests, repetition interruption, discovery without follower-count prerequisites | Reference only; video remains a later milestone |
| YouTube recommendations | [YouTube Help](https://support.google.com/youtube/answer/16533387?hl=en-GB) | Long-term satisfaction alongside viewing behavior | Reference only |
| Reddit recommendations | [Reddit Help](https://support.reddithelp.com/hc/en-us/articles/23511859482388-Reddits-Approach-to-Content-Recommendations) | Community context and recommendation controls | Linked as requested; page fetch unavailable during research, no unverified implementation details adopted |
| Rust transport | [Tonic 0.14.6](https://docs.rs/tonic/0.14.6/tonic/) | Versioned gRPC, request deadlines and bounded message sizes | MIT dependency; exact transitive versions in Cargo.lock |
| Distributed retrieval | [Qdrant distributed deployment](https://qdrant.tech/documentation/operations/distributed_deployment/) | Distributed vector retrieval in the target architecture | Documentation reference; not installed in baseline |

All JanSetu ranking and integration code in this milestone is original. No external algorithm's code or model is vendored, so there is no copied-code NOTICE requirement from either reference repository. Tonic, Tokio, Prost and Go gRPC/protobuf retain their package licenses through the dependency manifests/lockfiles; distributable release packaging must include dependency licenses/notices. Reusing any future source/model requires recording its exact revision, file inventory, license and attribution before inclusion. Published reference weights are never treated as JanSetu's learned or production weights.
