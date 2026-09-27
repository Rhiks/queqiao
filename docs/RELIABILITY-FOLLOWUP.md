# Reliability follow-up after the 0e0faba review

The original review combines confirmed defects, proposed architecture, and
acceptance campaigns. This follow-up prioritizes demonstrated correctness and
resource ownership defects. It does not treat every proposed subsystem as a
required feature or claim unrun campaigns passed.

| Review workstream | Disposition and evidence |
| --- | --- |
| B0 runtime provenance | Existing build/wire identity and recorded process executable hashes cover the deployed three-node release. Preserve separate source and running-build records for every deployment. |
| B1 data integrity and completion | Reassembly boundaries, transactional ACK validation, true ACK progress and normal receive completion were repaired in 3044aee and da75a1a. |
| B2 connection ownership | Bulk idle epochs, lane shutdown, replacement snapshots and transactional JOIN admission were repaired in 89ff781, 8454f81 and 9c952b7. Keep the focused concurrency tests. |
| B3 protocol rollout | Wire v2 and distinct ALPN reject mixed peers at negotiation. Independent legacy and v2 services retain rollback paths. Full historical binary interoperability remains NOT_RUN; negotiation fixtures are not historical binaries. |
| B4 retained payload ownership | Fix the concrete gap between dequeue/reassembly and a blocked application write. Stream contiguous segments directly to the consumer, retaining their shared reservation until each write returns. Deterministic tests cover a full shared budget, callback failure, cleanup and reverse ACK progress. |
| UDP ownership and accounting | Existing 78d620f owns socket write deadlines and records the successful substrate. No extra DNS affinity/cache policy is added without a demonstrated fault. |
| B5/B6 recovery and performance | Existing generation/epoch and recovery tests cover the repaired state transitions. Do not add new outage machinery, draining lifetime policies, FEC/MTU/congestion tuning, scheduling or kernel changes without a measured bottleneck. Removing the aggregate receive copy follows directly from the ownership repair; no WAN throughput gain is claimed. |
| B7 multi-gateway selection | Keep the existing external routing choice. A second selector adds competing policy and unstable egress without evidence of benefit. Cross-VPS migration of existing TCP sessions is out of scope. |
| B8 trustworthy field evidence | Reject truncated HTTP responses, decode transfer framing before hashing, optionally require an independently known body digest, and distinguish missing resource measurements from convergence. Reuse field_soak rather than create another harness. |

Shared budgets remain opt-in through the existing MemoryLimits API. This change
does not introduce a desktop/server default memory policy or claim the payload
budget measures total process RSS. Explicit receive credits and per-identity
admission require separate design and workload evidence and are not added here.

## Validation scope

Run the affected multipath and PEP short tests, targeted race tests for blocked
application delivery/cleanup, and the field harness unit tests. A real-path
smoke establishes deployability only. Real device sleep, default-route network
outages and 24–72 hour soak are not executed in this fast follow-up. No permanent
background benchmark or periodic fault injector is installed.

A release is not considered deployed merely because its source is pushed.
Record actual running revisions and keep the currently selected user route
available during any rollout.
