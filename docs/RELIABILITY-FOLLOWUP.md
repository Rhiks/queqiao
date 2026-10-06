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

## Tokyo AI follow-up

A later review identified a concrete asymmetry: an idle bulk-pool connection
could be reused based only on its QUIC context, whereas control-pool reuse
requires an authenticated round trip. Bulk reuse now shares the same probe
implementation, with a separate remote-proof lifetime constant (the previous
control lifetime is unchanged). New handshakes and recent successful probes
skip another probe; expired entries are exclusively reserved before a bounded
check. Failure removes only that entry, preserving shared control siblings.
Focused tests cover failed-entry retirement, real probe success, hot reuse and
sibling round trips. The first authenticated response on a new control-pool
stream now also supplies proof, dated no later than that stream's creation.
Reading buffered traffic later cannot renew it, and a transport failure
invalidates evidence from previously opened streams. This preserves idle
validation without periodically probing a sequence of successful short flows.
It does not claim an active-ACK lease fast path or a measured WAN latency
improvement.

The local operator's AI route uses the existing router's category data and a
fixed Tokyo group. This deployment policy does not belong in generic transport
source. No application TLS interception or AI request retry is introduced.
The acknowledged-request idle watchdog regression already protects waiting
applications; it is retained rather than adding an AI-specific timeout.

Policer overdrive remains a known limitation, not a repaired defect. Changing
the congestion controller, aggregate pacing or FEC policy requires evidence
that the Tokyo workload encounters this bottleneck and a controlled comparison.
The proposed new AI scheduler/profile and active-ACK pool lease remain deferred
for the same reason. None are silently marked implemented.

## Coded stall recovery scope

A send stall temporarily escapes coded DATA onto the authenticated reliable
stream. The escape applies only to byte offsets outstanding when it began;
later exchanges retain their ordinary coding policy while those bytes recover.
Validated cumulative or selective acknowledgements covering that fixed frontier
end the recovery. A delayed acknowledgement therefore cannot disable coding for
the rest of an interactive flow. Already dispatched retries retain their
reliable-carrier snapshot even if recovery ends before their writer runs.
The stall thresholds, retry deadlines and wire format are unchanged.

While an optimistic OPEN is awaiting confirmation on a healthy original lane,
the client does not start a speculative stall-rescue JOIN. Such a JOIN could
overtake OPEN on a different stream and receive a terminal "unknown session"
answer before the session exists. The original OPEN deadline still bounds this
wait, and actual loss of the original lane still permits replacement recovery.
Once OPEN is confirmed, established-flow refusals retain their terminal meaning.

### Validation and remaining short-flow limit

The October 6 follow-up used Go 1.25.13 on Linux/amd64. The unchanged
small-exchange integration test passed five native and five race runs, with
known-path medians of 303--311 ms on the 300 ms, 42% erasure path. The warm-pool
test now covers six warm flows at its unchanged latency bound. Deterministic
regressions reproduce both the redundant probe and the premature JOIN refusal
when their respective fixes are removed.

The complete `go test -p=1 -count=1 -timeout 50m ./...` run finished with seven
environmental failures: five Unix-socket tests and two interface-enumeration
tests returned `operation not permitted` in the test container. All other
assertions passed in that run; this is not a claim of a green full suite or
cross-platform validation. Vet, staticcheck, formatting, changelog validation
and the 91 Python tests passed.

End-to-end short-flow latency under extreme erasure remains unresolved. Three
additional race runs of the 20-flow, 300 ms RTT, 45% erasure case completed all
flows but measured medians of 960, 1266 and 967 ms against its unchanged 900 ms
bound. OPEN still travels on the reliable stream, and packet traces confirm
that its loss can delay registration even when coded DATA has already arrived.
The test also omits failed response reads from its latency samples, so its
survivor median must be read together with completion counts. These fixes do
not protect OPEN with coding, relax that timing assertion, or establish a WAN
latency improvement.

## Close acknowledgement follow-up

A failed application write and a late source EOF could publish different close
sequences. The first local close now owns the immutable terminal sequence, and
normal FIN emission uses that same value. An authenticated, matching abort ACK
ends cancellation without crediting unscheduled bytes as delivered. The focused
regression reproduces the former sequence mismatch and checks that malformed
terminal sequences are still rejected. This shared flow change applies to both
clients and gateways; it does not promise equal RTT across access networks.
