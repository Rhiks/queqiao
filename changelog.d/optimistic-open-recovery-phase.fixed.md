Wait for OPEN confirmation before starting stall rescue, QUIC lane isolation,
or TCP bundle JOINs. An optimistic application request can otherwise trigger
an unknown-session refusal while the gateway is still resolving or connecting
to its destination, closing a flow that could have opened successfully. Keep
the original OPEN deadline and cancellation bounds while allowing early data.
