package io.yaver.wear

import android.content.Context
import com.google.android.gms.wearable.CapabilityClient
import com.google.android.gms.wearable.MessageClient
import com.google.android.gms.wearable.Node
import com.google.android.gms.wearable.Wearable
import kotlinx.coroutines.tasks.await

/**
 * Wear Data Layer transport — the DEFAULT path.
 *
 * The watch never talks to the runner directly in this mode. It sends a turn
 * (transcript / confirm / intent) over MessageClient to the paired Android phone
 * running the Yaver mobile app, which runs the REAL carVoiceCoding loop and
 * pushes a reply back on PATH_REPLY (handled by ReplyListenerService).
 *
 * Node discovery: prefer the node that advertises the [WatchProtocol.CAPABILITY_PHONE]
 * capability (i.e. an actually-installed Yaver phone app). Fall back to any
 * connected node only if no capability node is present. If there is NO reachable
 * node at all, [PhoneUnreachableException] is thrown so the caller can fall back
 * to standalone mode (or show "phone not reachable") — the watch must never hang.
 */
class PhoneBridge(context: Context) {

    private val messageClient: MessageClient = Wearable.getMessageClient(context)
    private val capabilityClient: CapabilityClient = Wearable.getCapabilityClient(context)
    private val nodeClient = Wearable.getNodeClient(context)

    /** Thrown when no Yaver-capable (or any) phone node is reachable. */
    class PhoneUnreachableException(message: String) : Exception(message)

    /**
     * Resolve the best phone node to send to.
     *
     * 1. Capability nodes (Yaver phone app installed + advertising). Among those,
     *    prefer a NEARBY node (BT/Wi-Fi direct) for lowest latency.
     * 2. If none advertise the capability, fall back to any connected node —
     *    the phone app may be present but the capability not yet synced.
     */
    suspend fun resolvePhoneNode(): Node {
        // (1) capability-advertising nodes
        val capNodes: Set<Node> = try {
            capabilityClient
                .getCapability(WatchProtocol.CAPABILITY_PHONE, CapabilityClient.FILTER_REACHABLE)
                .await()
                .nodes
        } catch (_: Throwable) {
            emptySet()
        }
        capNodes.firstOrNull { it.isNearby }?.let { return it }
        capNodes.firstOrNull()?.let { return it }

        // (2) any connected node
        val connected: List<Node> = try {
            nodeClient.connectedNodes.await()
        } catch (_: Throwable) {
            emptyList()
        }
        connected.firstOrNull { it.isNearby }?.let { return it }
        connected.firstOrNull()?.let { return it }

        throw PhoneUnreachableException("No paired Yaver phone reachable")
    }

    /** True iff a phone node is currently reachable. Used to decide transport. */
    suspend fun isPhoneReachable(): Boolean =
        runCatching { resolvePhoneNode() }.isSuccess

    // --- Outbound turns -----------------------------------------------------

    private suspend fun send(json: String) {
        val node = resolvePhoneNode()
        messageClient
            .sendMessage(node.id, WatchProtocol.PATH_TURN, WatchProtocol.bytes(json))
            .await()
    }

    /** Watch → Phone: a spoken command. Phone runs the loop, replies async. */
    suspend fun sendTranscript(text: String) =
        send(WatchProtocol.transcript(text))

    /** Watch → Phone: answer a confirm-needed prompt. */
    suspend fun sendConfirm(token: String, reply: WatchProtocol.ConfirmReply) =
        send(WatchProtocol.confirm(token, reply))

    /** Watch → Phone: a fixed one-tap intent (run-tests / deploy / status). */
    suspend fun sendIntent(intent: WatchProtocol.FixedIntent) =
        send(WatchProtocol.intent(intent))
}
