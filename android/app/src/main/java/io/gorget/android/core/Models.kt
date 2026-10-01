package io.gorget.android.core

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

val json = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    coerceInputValues = true
}

/** Mirrors client.Status in the Go core. */
@Serializable
data class Status(
    val state: String = "no_server",
    val error: String? = null,
    val notice: String? = null,
    @SerialName("server_url") val serverUrl: String = "",
    @SerialName("network_name") val networkName: String = "",
    val domain: String = "",
    @SerialName("login_url") val loginUrl: String? = null,
    @SerialName("login_code") val loginCode: String? = null,
    val self: SelfView? = null,
    val peers: List<PeerView> = emptyList(),
    @SerialName("exit_node_id") val exitNodeId: String = "",
    @SerialName("exit_warning") val exitWarning: String? = null,
    val prefs: Prefs = Prefs(),
    @SerialName("relay_url") val relayUrl: String? = null,
    @SerialName("relay_connected") val relayConnected: Boolean = false,
    @SerialName("relay_transport") val relayTransport: String? = null,
    val endpoints: List<String> = emptyList(),
    @SerialName("can_choose_exit") val canChooseExit: Boolean = true,
    @SerialName("forced_exit") val forcedExit: Boolean = false,
    @SerialName("kill_switch_enforced") val killSwitchEnforced: Boolean = false,
    val version: String = "",
) {
    val isRunning get() = state == STATE_RUNNING
    val isActive get() = state == STATE_RUNNING || state == STATE_CONNECTING || state == STATE_PENDING || state == STATE_BLOCKED
    /** The security rules the device breaks, one per line of the server's message. */
    val blockedReasons get() = (error ?: "").lines().filter { it.startsWith("- ") }.map { it.removePrefix("- ") }
    val exitNode get() = peers.firstOrNull { it.id == exitNodeId }

    companion object {
        const val STATE_NO_SERVER = "no_server"
        const val STATE_NEEDS_LOGIN = "needs_login"
        const val STATE_STOPPED = "stopped"
        const val STATE_CONNECTING = "connecting"
        const val STATE_RUNNING = "running"
        const val STATE_PENDING = "pending_approval"
        const val STATE_EXPIRED = "expired"
        const val STATE_DISABLED = "disabled"
        /** The server keeps this device off the network until it meets the organisation's security rules. */
        const val STATE_BLOCKED = "blocked"
    }
}

@Serializable
data class SelfView(
    val id: String = "",
    val name: String = "",
    val fqdn: String = "",
    val ipv4: String = "",
    val ipv6: String = "",
    val user: String = "",
    @SerialName("key_expires_at") val keyExpiresAt: Long = 0,
)

@Serializable
data class PeerView(
    val id: String,
    val name: String = "",
    val fqdn: String = "",
    val ipv4: String = "",
    val ipv6: String = "",
    val os: String = "",
    val user: String = "",
    val tags: List<String> = emptyList(),
    val online: Boolean = false,
    @SerialName("last_seen") val lastSeen: Long = 0,
    val direct: Boolean = false,
    val endpoint: String? = null,
    @SerialName("latency_ms") val latencyMs: Int = 0,
    @SerialName("rx_bytes") val rxBytes: Long = 0,
    @SerialName("tx_bytes") val txBytes: Long = 0,
    @SerialName("last_handshake") val lastHandshake: Long = 0,
    @SerialName("exit_node") val exitNode: Boolean = false,
    val gateway: Boolean = false,
    val routes: List<String> = emptyList(),
    /** The connection to this device is also protected against future quantum computers. */
    @SerialName("post_quantum") val postQuantum: Boolean = false,
)

@Serializable
data class Prefs(
    @SerialName("exit_node_id") val exitNodeId: String = "",
    @SerialName("allow_lan") val allowLan: Boolean = true,
    @SerialName("accept_routes") val acceptRoutes: Boolean = true,
    @SerialName("use_dns") val useDns: Boolean = true,
    @SerialName("no_post_quantum") val noPostQuantum: Boolean = false,
    @SerialName("want_running") val wantRunning: Boolean = false,
)

/** Mirrors client.TUNConfig. */
@Serializable
data class TunConfig(
    val addresses: List<String>? = null,
    val routes: List<String>? = null,
    @SerialName("excluded_routes") val excludedRoutes: List<String>? = null,
    @SerialName("routes_minus_excluded") val routesMinusExcluded: List<String>? = null,
    val dns: List<String>? = null,
    @SerialName("search_domains") val searchDomains: List<String>? = null,
    val mtu: Int = 1280,
    @SerialName("full_tunnel") val fullTunnel: Boolean = false,
    @SerialName("allow_lan") val allowLan: Boolean = true,
)

@Serializable
data class LoginStart(val url: String, val code: String)
