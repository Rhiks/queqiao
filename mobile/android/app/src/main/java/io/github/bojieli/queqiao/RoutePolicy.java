package io.github.bojieli.queqiao;

import android.net.VpnService;

import java.math.BigInteger;
import java.net.InetAddress;
import java.net.UnknownHostException;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;

final class RoutePolicy {
    // The same list as RoutePlan.localNetworks on iOS. Neither client can see
    // the other, so scripts/test_mobile_route_parity.py compares them.
    private static final String[] LOCAL_EXCLUSIONS = {
            "10.0.0.0/8",
            "100.64.0.0/10",
            "127.0.0.0/8",
            "169.254.0.0/16",
            "172.16.0.0/12",
            "192.168.0.0/16",
            "::1/128",
            "fc00::/7",
            "fe80::/10"
    };

    private RoutePolicy() {
    }

    static void apply(VpnService.Builder builder, TrafficPolicy policy) {
        if (policy == TrafficPolicy.ALL_TRAFFIC) {
            builder.addRoute("0.0.0.0", 0);
            builder.addRoute("::", 0);
            return;
        }
        try {
            for (RouteSpec route : routesExcludingLocalNetworks()) {
                builder.addRoute(route.address(), route.prefixLength);
            }
        } catch (UnknownHostException exception) {
            throw new IllegalStateException("The built-in local-network policy is invalid", exception);
        }
    }

    static List<RouteSpec> routesExcludingLocalNetworks() throws UnknownHostException {
        List<RouteSpec> routes = new ArrayList<>();
        routes.add(RouteSpec.parse("0.0.0.0/0"));
        routes.add(RouteSpec.parse("::/0"));
        for (String encoded : LOCAL_EXCLUSIONS) {
            RouteSpec exclusion = RouteSpec.parse(encoded);
            List<RouteSpec> next = new ArrayList<>();
            for (RouteSpec route : routes) {
                next.addAll(route.subtract(exclusion));
            }
            routes = next;
        }
        return Collections.unmodifiableList(routes);
    }

    static final class RouteSpec {
        final BigInteger network;
        final int prefixLength;
        final int bitCount;

        RouteSpec(BigInteger network, int prefixLength, int bitCount) {
            this.bitCount = bitCount;
            this.prefixLength = prefixLength;
            this.network = normalize(network, prefixLength, bitCount);
        }

        static RouteSpec parse(String encoded) throws UnknownHostException {
            int separator = encoded.lastIndexOf('/');
            if (separator <= 0 || separator == encoded.length() - 1) {
                throw new UnknownHostException("Invalid CIDR: " + encoded);
            }
            InetAddress address = InetAddress.getByName(encoded.substring(0, separator));
            int bitCount = address.getAddress().length * 8;
            final int prefixLength;
            try {
                prefixLength = Integer.parseInt(encoded.substring(separator + 1));
            } catch (NumberFormatException exception) {
                throw new UnknownHostException("Invalid CIDR prefix: " + encoded);
            }
            if (prefixLength < 0 || prefixLength > bitCount) {
                throw new UnknownHostException("Invalid CIDR prefix: " + encoded);
            }
            return new RouteSpec(new BigInteger(1, address.getAddress()), prefixLength, bitCount);
        }

        String address() throws UnknownHostException {
            byte[] raw = toFixedWidth(network, bitCount / 8);
            return InetAddress.getByAddress(raw).getHostAddress();
        }

        boolean contains(String address) throws UnknownHostException {
            InetAddress parsed = InetAddress.getByName(address);
            if (parsed.getAddress().length * 8 != bitCount) {
                return false;
            }
            BigInteger value = new BigInteger(1, parsed.getAddress());
            return normalize(value, prefixLength, bitCount).equals(network);
        }

        List<RouteSpec> subtract(RouteSpec exclusion) {
            if (bitCount != exclusion.bitCount || !overlaps(exclusion)) {
                return Collections.singletonList(this);
            }
            if (exclusion.prefixLength <= prefixLength) {
                return Collections.emptyList();
            }
            int childPrefix = prefixLength + 1;
            BigInteger childSize = BigInteger.ONE.shiftLeft(bitCount - childPrefix);
            RouteSpec first = new RouteSpec(network, childPrefix, bitCount);
            RouteSpec second = new RouteSpec(network.add(childSize), childPrefix, bitCount);
            List<RouteSpec> result = new ArrayList<>();
            result.addAll(first.subtract(exclusion));
            result.addAll(second.subtract(exclusion));
            return result;
        }

        private boolean overlaps(RouteSpec other) {
            int commonPrefix = Math.min(prefixLength, other.prefixLength);
            return normalize(network, commonPrefix, bitCount)
                    .equals(normalize(other.network, commonPrefix, bitCount));
        }

        private static BigInteger normalize(BigInteger value, int prefixLength, int bitCount) {
            if (prefixLength == 0) {
                return BigInteger.ZERO;
            }
            int hostBits = bitCount - prefixLength;
            return value.shiftRight(hostBits).shiftLeft(hostBits);
        }

        private static byte[] toFixedWidth(BigInteger value, int width) {
            byte[] encoded = value.toByteArray();
            byte[] result = new byte[width];
            int sourceOffset = Math.max(0, encoded.length - width);
            int count = Math.min(width, encoded.length);
            System.arraycopy(encoded, sourceOffset, result, width - count, count);
            return result;
        }
    }
}
