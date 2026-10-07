Do not treat small application-limited exchanges or startup measurements as
a shared path capacity ceiling. Keep them as useful bandwidth seeds, and split
capacity only among senders actually using their congestion window, so idle
connections no longer throttle a later download. Preserve measured congestion
sharing when multiple saturated senders compete.
