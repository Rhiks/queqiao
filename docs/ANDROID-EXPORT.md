# Android export mode

The released Android app is not a VPN. It enrolls a device with a Queqiao
provider, holds the identity, keeps the certificate renewed, and serves the
gateway to the rest of the phone as one authenticated SOCKS5 endpoint on
loopback. Whichever client the user already trusts with routing — v2rayNG,
mihomo, sing-box — owns the tunnel, the rules, and DNS, and treats Queqiao as
one outbound among many.

This follows the project's own scope rule in [Vision](VISION.md): Queqiao
supplies the optimized paired data plane, and a larger overlay supplies
discovery, routing, policy, and mesh coordination. A full-device Android tunnel
put the data plane in competition with mature routing clients over rules it
has no engine for — `mobile/core/packetstack.go` has exactly one outbound, so
there was never anything for a routing rule to select.

The full-device tunnel still exists, in the debug build only. See
[The debug tunnel](#the-debug-tunnel).

## What the released app declares

- `INTERNET`, `POST_NOTIFICATIONS`, `FOREGROUND_SERVICE`,
  `FOREGROUND_SERVICE_SPECIAL_USE`, `ACCESS_NETWORK_STATE`, and `CAMERA`.
  `ACCESS_NETWORK_STATE` is read-only and serves one question — whether
  another app's VPN is carrying Queqiao's own uplink — described under
  [the exclusion check](#first-exclude-queqiao-from-the-consumers-tunnel).
  `CAMERA` is optional and is held only by the invitation scanner while it
  is in front; the QR code is decoded in process by the Go core, and the
  camera feature is declared as not required so the app installs without one.
- One service, `QueqiaoProxyService`, type `specialUse`, with a subtype
  justification naming what it actually does: it serves a local SOCKS5 endpoint
  for a network client the user configured, and the connection has to outlive
  the app's foreground because that client relays through it continuously.
- No `BIND_VPN_SERVICE`, no `android.net.VpnService` intent filter, and no
  always-on metadata. CI asserts this against the assembled release APK with
  `aapt2 dump xmltree`, so a stray manifest merge cannot reintroduce them
  quietly. The same step pins the permission list above exactly: a permission
  arriving from a merged library manifest fails the build rather than shipping,
  and widening the list is a deliberate edit with a reason attached.

Google Play's Organization requirement is scoped to *apps approved to use the
`VpnService` class*, which a release build declaring no `VpnService` is not.
That removes the one blocker known by name; it is not a promise of publication,
because the `specialUse` justification is itself a review surface and Play has
separate policy pages. Treat it as one obstacle removed, not as eligibility.

## The endpoint

| Property | Value |
| --- | --- |
| Address | `127.0.0.1:1080` by default; the port is changeable in Settings |
| Authentication | SOCKS5 username/password, RFC 1929, required |
| Credentials | Generated per install from `SecureRandom`, URL-safe Base64 |
| Storage | The Keystore-backed `SecureStore`, alongside the device key |
| UDP | `UDP ASSOCIATE`, so QUIC and DNS-over-UDP work through the outbound |

The listener binds loopback and refuses anything else — the Go core rejects a
non-loopback listen address rather than trusting the caller, which is the same
invariant the desktop SOCKS listener holds.

Loopback is shared with every other app on the device. It is not a private
channel: the credentials are the only thing between the gateway and any app
that happens to guess the port, which is why authentication is mandatory here
while the desktop listener has none. They never leave the device. Settings
offers **Copy endpoint and credentials**, **Change port**, and **Regenerate
credentials**; regenerating breaks every configured client until each is
updated, which is the point of the action.

## First: exclude Queqiao from the consumer's tunnel

Do this before anything else. Every client names it differently — per-app
proxy, access control, split tunnelling — but all of them offer it.

| Client | Where |
| --- | --- |
| v2rayNG / Xray | Settings, then Per-app proxy: turn it on, choose bypass mode, select Queqiao |
| mihomo / ClashMetaForAndroid | Settings, then Access control: choose deny selected apps, check Queqiao |
| sing-box / NekoBox | Settings, then Per-app proxy: choose exclude mode, select Queqiao |

Skip it and the consumer's TUN captures Queqiao's own uplink, sends it into its
own outbound, and that outbound is this listener. Traffic then loops until it
times out rather than failing outright, which is the worst of both: no error,
no throughput.

The app checks whether it happened. Android answers the question directly,
because the default network it reports is *per-UID*: a VPN that excluded
Queqiao is not Queqiao's default network, so `TRANSPORT_VPN` on this app's own
active network means the exclusion was not applied. `VpnExclusion` asks at
connect time and then keeps a default-network callback registered, because the
usual ordering is Queqiao first and the consumer's tunnel second.

The answer is advisory and never blocks a connection. A VPN carrying Queqiao's
uplink is not proof of a loop: a corporate VPN the gateway is reachable through
is a legitimate setup, and so is a consumer client whose rules send the gateway
address direct. What the check buys is a named cause instead of a guess — the
notification reads `VPN not excluded`, the log carries the instruction, and a
failed connection test leads with the diagnosis rather than a bare timeout.

**Test connection** remains the explicit check, and the one to run after
configuring a client. The probe measures DNS, transport setup, mutual TLS,
device authorization, protocol negotiation, and one authenticated control round
trip, and opens no remote destination. A loop shows there as a provider that
cannot be reached — loudly, and before any real traffic is affected.

The debug full tunnel allows the test while connected for a different reason:
it excludes the app's own UID from the interface it installs, so the probe
leaves by the device's ordinary route and still measures the provider rather
than the tunnel.

## Client configuration

Queqiao is an ordinary authenticated SOCKS5 proxy to these clients. Keep UDP
enabled on the outbound or QUIC-based sites silently fall back to TCP. The app
renders each of these with the live address and credentials filled in and a
copy button; the forms below use placeholders.

### v2rayNG / Xray

```json
{
  "protocol": "socks",
  "tag": "queqiao",
  "settings": {
    "servers": [{
      "address": "127.0.0.1",
      "port": 1080,
      "users": [{
        "user": "USERNAME",
        "pass": "PASSWORD"
      }]
    }]
  }
}
```

### mihomo / ClashMetaForAndroid

```yaml
proxies:
  - name: queqiao
    type: socks5
    server: 127.0.0.1
    port: 1080
    username: "USERNAME"
    password: "PASSWORD"
    udp: true
```

### sing-box / NekoBox

```json
{
  "type": "socks",
  "tag": "queqiao",
  "server": "127.0.0.1",
  "server_port": 1080,
  "version": "5",
  "username": "USERNAME",
  "password": "PASSWORD"
}
```

Point rules at the outbound the same way as any other proxy. Queqiao inherits
the client's entire rule engine, including its DNS handling — which dissolves
the mobile split-DNS problem rather than solving it, because Queqiao never
resolves anything on the consumer's behalf.

## What export mode does not do

- No routing rules, no per-app policy, no DNS policy. Those belong to the
  consumer, and duplicating them here would mean two engines disagreeing.
- No `protect()`. Without a `VpnService` there is no interface to be exempt
  from, and the core is not told otherwise — the exclusion above is what
  replaces it.
- No packet counters. `MetricsJSON` reports `"mode": "proxy"` so a UI can
  tell "no packet engine in this product" from "idle", and the app shows the
  listen address and connection state instead.

Certificate maintenance is independent of the packet stack and runs unchanged,
so hourly renewal keeps working in export mode.

## The debug tunnel

The full-device `VpnService` tunnel lives in `app/src/debug/` and nowhere else:
`QueqiaoVpnService`, `RoutePolicy`, `VpnTunnelController`, and a manifest
overlay that re-adds `BIND_VPN_SERVICE` and the `android.net.VpnService`
intent filter. It is retained as the one vehicle that drives the Go packet
stack end to end on a real device — TUN file descriptor in, TCP and UDP flows
out — and is never published.

`build.gradle` gives debug an `applicationIdSuffix ".debug"`, so both variants
install side by side. The seam between them is `TunnelModes.java`, which exists
once per build type and lists the modes that build offers. Keeping it a whole
file rather than a build-config flag means the release build never compiles the
tunnel at all, which is what makes the CI manifest assertion a check on a
property that is already structurally true rather than the only thing holding
it.

## Device qualification

The end-to-end check this mode needs, on hardware:

1. Install the debug APK, select export mode, connect, and confirm the
   notification shows the listen address.
2. Configure v2rayNG with Queqiao excluded from its tunnel; confirm egress
   through the gateway and that `UDP ASSOCIATE` carries UDP.
3. Remove the exclusion and confirm the failure is loud rather than a silent
   slow degrade: the notification gains `VPN not excluded` while the session is
   still up, and Test connection reports an unreachable provider with the
   exclusion named as the likely cause.
4. Re-apply the exclusion without disconnecting and confirm the warning clears
   on its own — the default-network callback, not a reconnect, is what notices.
