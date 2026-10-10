Keep exact rolling bandwidth maxima without rescanning the full observation
history on every QUIC acknowledgement. This reduces shared path accounting
work during busy transfers while preserving capacity ceilings, startup seeds,
and the existing ten-second expiry window.
