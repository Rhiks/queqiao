The Android full tunnel routes the way the iOS tunnel does: a routing mode,
bypasses for local networks, the bundled Chinese address set and up to 256
hand-entered routes, and a rule-list editor with the same lint and the same
China preset, which keeps Chinese sites direct by name as well as by address.
Hand-entered routes are read as numeric addresses only, so a typo is refused
instead of being looked up. The address set installs as routes on Android 13
and later; earlier releases keep those addresses direct through a
`GEOIP,CN,DIRECT` rule. Routing is edited while disconnected and applies at
the next connect.
