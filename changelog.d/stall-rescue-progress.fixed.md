Queued stall notifications are rechecked against current delivery before they
can rotate a shared QUIC connection. Rescue JOIN success no longer resets the
retry backoff by itself: without new data acknowledgements, both successful
and failed rounds retain exponential pacing. Real acknowledgement progress
restores prompt rescue without closing sibling flows or disabling recovery.
