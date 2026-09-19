The Android app ships the full-device tunnel, and it is the default connection
mode for a new install. Until now a released build could only export a local
SOCKS5 endpoint for another VPN client, which left a phone with no such client
unable to connect at all. Both modes ship and are chosen under Settings; an
install that had already picked one keeps it. Because the release build now
declares a `VpnService`, a Google Play listing needs an Organization account
and Play's VPN declaration; direct APK distribution is unaffected.
