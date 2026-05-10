package io.yaver.mobile

// Custom OkHttpClient factory wired into React Native's global
// networking stack (OkHttpClientProvider) at app start. The only
// behavioural change vs RN's default builder: DNS lookups return
// IPv4 addresses BEFORE IPv6.
//
// Why: OkHttp 4.x (shipped with RN 0.81) uses *serial* Happy Eyeballs
// — it dials the first address from the lookup list, waits up to
// connectTimeout for it to succeed, then falls back to the next.
// `Dns.SYSTEM` returns IPv6 first on dual-stack networks. On Wi-Fi
// where the router advertises an IPv6 prefix but the upstream ISP
// drops IPv6 packets (a real-world misconfiguration we hit on an
// AirTies Air4960R: tablet has 2a00:1d34:.../64 but every external
// IPv6 hop drops), every fetch stalls 15-30s on the IPv6 attempt
// before OkHttp falls back to IPv4. The JS-side AbortControllers
// (10s in mobile/src/lib/auth.ts:113) fire long before the fallback
// lands, so users see "Couldn't reach the auth server" / "AbortError"
// on EVERY fetch — auth/validate, /devices/refresh, backend-config,
// the lot.
//
// Reordering to IPv4-first eliminates the dependency on broken IPv6
// routes for dual-stack hosts. IPv6-only hosts (no A record) still
// work since we keep the IPv6 entries in the list — they just come
// after the v4s. This is the same workaround used by other RN apps
// running on consumer routers (search: "react-native okhttp ipv4
// first happy eyeballs").
//
// Connection timeouts are kept at 0 (the RN default — "no timeout")
// because the JS layer manages its own AbortController per call;
// imposing an OkHttp-level timeout would just race the JS abort and
// produce confusing errors.

import com.facebook.react.modules.network.OkHttpClientFactory
import com.facebook.react.modules.network.OkHttpClientProvider
import java.net.Inet4Address
import java.net.Inet6Address
import java.net.InetAddress
import java.net.UnknownHostException
import java.util.concurrent.TimeUnit
import okhttp3.Dns
import okhttp3.OkHttpClient

class YaverOkHttpFactory : OkHttpClientFactory {
  override fun createNewNetworkModuleClient(): OkHttpClient {
    return OkHttpClientProvider.createClientBuilder()
        .dns(Ipv4FirstDns)
        // Bound the connect timeout so a single dead address doesn't
        // monopolize the whole AbortController budget. JS-side aborts
        // fire at 10s — give each address attempt 6s so we can dial
        // v4 even if a stale v6 entry is still in the list. readTimeout
        // stays at 0 to match RN's default (the JS callers manage it).
        .connectTimeout(6, TimeUnit.SECONDS)
        .build()
  }
}

private object Ipv4FirstDns : Dns {
  @Throws(UnknownHostException::class)
  override fun lookup(hostname: String): List<InetAddress> {
    val addrs = Dns.SYSTEM.lookup(hostname)
    if (addrs.size <= 1) return addrs
    val v4 = ArrayList<InetAddress>(addrs.size)
    val v6 = ArrayList<InetAddress>(addrs.size)
    for (a in addrs) {
      when (a) {
        is Inet4Address -> v4.add(a)
        is Inet6Address -> v6.add(a)
        else -> v4.add(a) // unknown family: treat as v4 for safety
      }
    }
    v4.addAll(v6)
    return v4
  }
}
