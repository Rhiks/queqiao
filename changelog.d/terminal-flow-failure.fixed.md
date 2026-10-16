Notify the peer with a bounded RESET before closing a flow whose local
endpoint fails or whose idle/lifetime limit expires. Preserve the original
local error, avoid unnecessary recovery JOINs against removed sessions, and
keep normal half-close, lane recovery, and sibling pooled flows unchanged.
