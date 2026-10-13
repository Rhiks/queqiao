Upgrade the desktop and mobile Go toolchains to 1.26.9 and x/net to
0.60.0, with the required crypto, sys, and text dependencies, to
address the reported standard-library and HTTP/2 vulnerabilities. Align
runtime dependency locks and third-party notices with the resolved modules.
Migrate the HTTP/2 measurement modes to the standard-library APIs so the
security dependency update passes static checks without suppressions.
