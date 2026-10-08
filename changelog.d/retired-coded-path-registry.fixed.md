Prevent late subscriber cleanup from recreating a retired coded transport and
retaining closed QUIC connections. Reject subscriptions on closed paths, and
distinguish genuine macOS uplink loss from healthy interface/address/route
refresh notifications so those refreshes do not reset active connection pools.
Heap allocation diagnostics are available only on loopback metrics listeners.
