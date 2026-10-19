Authenticated lane recovery now replays the same observed close state for live
and retained sessions. A destination full-close no longer invents a final ACK
when the peer's FIN was never received, and its existing abort is replayed even
when no normal FIN was sent. This prevents false final-ACK sequence errors and
lost cancellation after JOIN without relaxing acknowledgement validation.
