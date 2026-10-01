package io.gorget.android.vpn

import android.app.PendingIntent
import android.content.Intent
import android.net.ConnectivityManager
import android.net.IpPrefix
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.VpnService
import android.os.Build
import android.util.Log
import io.gorget.android.core.AppPrefs
import io.gorget.android.core.GorgetCore
import io.gorget.android.core.TunConfig
import io.gorget.android.ui.MainActivity
import kotlinx.coroutines.launch
import java.net.InetAddress

/**
 * Hosts the tunnel. The Go core asks this service (through GorgetCore) to create the
 * VPN interface and to exclude its own sockets from it. Supports Android's
 * Always-On VPN: the system starts us with the VpnService action.
 */
class GorgetVpnService : VpnService() {

    companion object {
        const val ACTION_CONNECT = "io.gorget.android.CONNECT"
        const val ACTION_DISCONNECT = "io.gorget.android.DISCONNECT"
        private const val TAG = "GorgetVpn"
    }

    private var cm: ConnectivityManager? = null
    private var lastNetwork: Network? = null

    private val networkCallback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            if (network != lastNetwork) {
                lastNetwork = network
                setUnderlyingNetworks(arrayOf(network))
                report(network, cm?.getLinkProperties(network))
            }
        }

        override fun onLinkPropertiesChanged(network: Network, lp: LinkProperties) {
            report(network, lp)
        }

        override fun onLost(network: Network) {
            if (network == lastNetwork) lastNetwork = null
        }
    }

    private fun report(network: Network, lp: LinkProperties?) {
        val caps = cm?.getNetworkCapabilities(network)
        if (caps?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true) return
        val addrs = lp?.linkAddresses?.mapNotNull { it.address?.hostAddress?.substringBefore('%') }.orEmpty()
        GorgetCore.networkChanged(addrs.joinToString(","))
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_DISCONNECT -> {
                GorgetCore.scope.launch {
                    GorgetCore.down()
                    stopTunnelService()
                }
                return START_NOT_STICKY
            }
            // ACTION_CONNECT from the app, SERVICE_INTERFACE from Always-On VPN, or a restart.
            else -> {
                if (!GorgetCore.isRegistered()) {
                    stopSelf()
                    return START_NOT_STICKY
                }
                GorgetCore.attach(this)
                registerNetworkCallback()
                GorgetCore.scope.launch { GorgetCore.up() }
            }
        }
        return START_STICKY
    }

    private fun stopTunnelService() {
        unregisterNetworkCallback()
        GorgetCore.detach(this)
        stopSelf()
    }

    private fun registerNetworkCallback() {
        if (cm != null) return
        cm = getSystemService(ConnectivityManager::class.java)
        runCatching { cm?.registerDefaultNetworkCallback(networkCallback) }
            .onFailure { Log.w(TAG, "network callback: ${it.message}") }
    }

    private fun unregisterNetworkCallback() {
        runCatching { cm?.unregisterNetworkCallback(networkCallback) }
        cm = null
    }

    override fun onRevoke() {
        // Another VPN took over or the user revoked permission.
        GorgetCore.down()
        stopTunnelService()
    }

    override fun onDestroy() {
        unregisterNetworkCallback()
        GorgetCore.detach(this)
        super.onDestroy()
    }

    /** Builds the VPN interface; returns a detached fd owned by the Go core, or -1. */
    fun establish(cfg: TunConfig): Int {
        val addresses = cfg.addresses.orEmpty()
        if (addresses.isEmpty()) return -1 // the core closes its fd itself
        return try {
            val b = Builder()
                .setSession("Gorget")
                .setMtu(cfg.mtu)
                .setConfigureIntent(
                    PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE),
                )
            for (a in addresses) {
                val (ip, len) = splitPrefix(a)
                b.addAddress(ip, len)
            }
            if (Build.VERSION.SDK_INT >= 33) {
                cfg.routes.orEmpty().forEach { r -> val (ip, len) = splitPrefix(r); b.addRoute(ip, len) }
                cfg.excludedRoutes.orEmpty().forEach { r ->
                    val (ip, len) = splitPrefix(r)
                    b.excludeRoute(IpPrefix(InetAddress.getByName(ip), len))
                }
            } else {
                cfg.routesMinusExcluded.orEmpty().forEach { r -> val (ip, len) = splitPrefix(r); b.addRoute(ip, len) }
            }
            cfg.dns.orEmpty().forEach { b.addDnsServer(it) }
            cfg.searchDomains.orEmpty().forEach { b.addSearchDomain(it) }
            if (Build.VERSION.SDK_INT >= 29) b.setMetered(false)
            applySplitTunnel(b)
            val pfd = b.establish() ?: return -1
            pfd.detachFd()
        } catch (e: Exception) {
            Log.e(TAG, "establish failed", e)
            -1
        }
    }

    private fun applySplitTunnel(b: Builder) {
        val prefs = GorgetCore.appPrefs
        val apps = prefs.splitApps
        when (prefs.splitMode) {
            AppPrefs.SplitMode.ONLY_SELECTED -> apps.forEach { runCatching { b.addAllowedApplication(it) } }
            AppPrefs.SplitMode.EXCEPT_SELECTED -> apps.forEach { runCatching { b.addDisallowedApplication(it) } }
            AppPrefs.SplitMode.ALL -> {}
        }
    }

    private fun splitPrefix(p: String): Pair<String, Int> {
        val i = p.indexOf('/')
        if (i < 0) return p to (if (p.contains(':')) 128 else 32)
        return p.substring(0, i) to p.substring(i + 1).toInt()
    }
}
