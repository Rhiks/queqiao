Path prewarm now shares its caller's cancellation and deadline through padding,
echo reads and measurement waiting. Canceled work stops promptly instead of
sending unnecessary padding or waiting out the remaining probe budget. Normal
measurement volume, echo validation and pooled sibling connections are retained.
