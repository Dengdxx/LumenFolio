package org.lumenfolio.android

import java.net.HttpURLConnection
import java.net.Proxy
import java.net.URI

internal object FrameClient {
    const val PORT = 8080

    /** RECEIVED 和 NOT_QUEUED 都表示帧已写入，后者需要单独提示。 */
    enum class Status { RECEIVED, NOT_QUEUED, FAILED }

    data class Reply(val status: Status, val message: String)

    /**
     * 把一整帧直传给相框。固件要求固定长度的原始帧，HTTP 202 只确认已写入。
     */
    fun send(host: String, frame: ByteArray): Reply {
        val url = URI("http", null, host, PORT, "/frame", null, null).toURL()
        val connection = url.openConnection(Proxy.NO_PROXY) as HttpURLConnection
        try {
            connection.requestMethod = "POST"
            connection.instanceFollowRedirects = false
            connection.connectTimeout = 5_000
            connection.readTimeout = 30_000
            connection.doOutput = true
            connection.setRequestProperty("Content-Type", "application/octet-stream")
            connection.setFixedLengthStreamingMode(frame.size)
            connection.outputStream.use { it.write(frame) }

            val status = connection.responseCode
            val stream = if (status >= 400) connection.errorStream else connection.inputStream
            val detail = stream?.bufferedReader(Charsets.UTF_8)?.use { it.readText().trim() }.orEmpty()
            return when {
                status != HttpURLConnection.HTTP_ACCEPTED -> Reply(Status.FAILED, "HTTP $status $detail".trim())
                "还没排上刷新" in detail -> Reply(Status.NOT_QUEUED, "画面已写入，刷新未排队。")
                else -> Reply(Status.RECEIVED, "相框已接收。整帧刷新约 35 秒，请查看屏幕。")
            }
        } finally {
            connection.disconnect()
        }
    }
}
