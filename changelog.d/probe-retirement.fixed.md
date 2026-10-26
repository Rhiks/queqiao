Path prewarm distinguishes transport interruption from an invalid authenticated
echo. Address loss, local connection retirement and canceled streams no longer
count as gateway protocol violations or reset recovered pools. Actual probe
violations retire only their own control generation, preserving newer control
and unrelated bulk connections while retaining strict echo validation.
