A gateway that stopped reading in the middle of a lane JOIN no longer wedges
that flow's lane admission slots for the flow's life: the OPEN_OK
acknowledgement is now written under a deadline, and a staged lane that
somehow stays stuck past the rescue-race window is evictable by the next
JOIN instead of counting against the ceiling forever. Permanent refusals --
a flow already committed to TCP fallback, a closed flow, a duplicate lane id
-- are now answered with the reset codes that mean so, rather than the
retryable lane-capacity answer the client would otherwise retry for the
flow's whole life. The client in turn stops believing a capacity answer that
repeats without a single successful rescue: after eight consecutive refusals
the flow fails fast so the application reconnects on a fresh session, after
three an AUTO flow commits to TCP fallback as its remaining escape, a peer
that says the flow already lives on TCP is taken at its word, and a TCP
bundle's widening pause after one capacity refusal now expires with the
recovery cooldown instead of lasting the flow's life.
