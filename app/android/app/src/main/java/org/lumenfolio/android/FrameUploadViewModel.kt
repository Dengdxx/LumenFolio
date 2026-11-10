package org.lumenfolio.android

import android.app.Application
import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import android.provider.OpenableColumns
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.referentialEqualityPolicy
import androidx.compose.runtime.setValue
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import java.io.IOException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.job
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.lumenfolio.mobilepanel.Crop
import org.lumenfolio.mobilepanel.DeviceSession
import org.lumenfolio.mobilepanel.FrameTransferListener
import org.lumenfolio.mobilepanel.Mobilepanel
import org.lumenfolio.mobilepanel.OTAListener
import org.lumenfolio.mobilepanel.Picture
import org.lumenfolio.mobilepanel.Rendered

/** 取景界面使用缩略图，采样始终使用同一张 Go 原图及其像素坐标。 */
internal data class EditorPicture(
    val picture: Picture,
    val bitmap: Bitmap,
    val width: Int,
    val height: Int,
    val crop: Crop,
    val name: String,
)

/** 开屏之后的工作区。没有进入时停在连接页。 */
internal enum class EntryMode { GATE, WIFI, BLUETOOTH, OFFLINE }

internal enum class FrameDestination(val source: String, val label: String) {
    SD("sd", "SD 卡"),
    FLASH("artifacts", "Flash artifacts"),
}

internal data class FrameUploadProgress(val stage: String, val percent: Int, val message: String)
internal data class FrameUploadReply(val message: String, val error: Boolean = false)
internal data class FrameBluetoothSelection(val name: String, val address: String)

/** 确认界面展示的版本和上传的数据来自同一份已选镜像。 */
internal class SelectedFirmware(val name: String, val version: String, val data: ByteArray)

internal data class FirmwareFeedback(val message: String, val fraction: Float? = null, val error: Boolean = false)

internal class FrameUploadViewModel(application: Application) : AndroidViewModel(application) {
    private val preferences = application.getSharedPreferences("lumenfolio", Context.MODE_PRIVATE)
    private val contentResolver = application.contentResolver

    var editor by mutableStateOf<EditorPicture?>(null, referentialEqualityPolicy())
        private set
    var algorithm by mutableStateOf(0)
        private set
    private var showingPreview by mutableStateOf(false)
    val previewMode: Boolean get() = showingPreview
    var preview by mutableStateOf<Bitmap?>(null)
        private set
    var previewing by mutableStateOf(false)
        private set
    var address by mutableStateOf(preferences.getString("frame_address", "").orEmpty())
        private set
    var frameIdentity by mutableStateOf(preferences.getString("frame_identity", "").orEmpty())
        private set
    var identityNotice by mutableStateOf<String?>(null)
        private set
    var bluetooth by mutableStateOf<FrameBluetoothSelection?>(null)
        private set

    /**
     * 当前相框会话。手填地址与蓝牙共用同一个句柄，操作不再依赖 host 字符串。
     * gobind 生成的 equals 恒为真，状态必须按引用比较，否则换会话不会触发重组。
     */
    var session by mutableStateOf<DeviceSession?>(null, referentialEqualityPolicy())
        private set
    var entry by mutableStateOf(EntryMode.GATE)
        private set

    /** 会话对应的目标，用于判断地址变化后是否需要重建。 */
    private var sessionTarget = ""
    var bluetoothConnecting by mutableStateOf(false)
        private set
    var bluetoothError by mutableStateOf<String?>(null)
        private set
    var uploadDestination by mutableStateOf<FrameDestination?>(null)
        private set
    private var manualDestination = false
    var busy by mutableStateOf(false)
        private set
    var sending by mutableStateOf(false)
        private set
    var uploadProgress by mutableStateOf<FrameUploadProgress?>(null)
        private set
    var reply by mutableStateOf<FrameUploadReply?>(null)
        private set
    var message by mutableStateOf<String?>(null)
        private set
    var firmware by mutableStateOf<SelectedFirmware?>(null)
        private set
    var firmwareFeedback by mutableStateOf<FirmwareFeedback?>(null)
        private set


    private data class RenderCache(
        val editor: EditorPicture,
        val algorithm: Int,
        val rendered: Rendered,
    )

    private var cachedRender: RenderCache? = null
    private var previewJob: Job? = null
    private val imageWork = Mutex()

    fun openImage(uri: Uri) {
        runOperation(failure = "没打开图片") {
            val (bytes, name) = withContext(Dispatchers.IO) {
                val name = try {
                    contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)
                        ?.use { cursor ->
                            val index = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                            if (index >= 0 && cursor.moveToFirst()) cursor.getString(index) else null
                        }
                } catch (_: IllegalArgumentException) {
                    null
                }
                val bytes = contentResolver.openInputStream(uri)?.use { it.readBytes() }
                    ?: throw IOException("无法读取选中的图片")
                bytes to (name ?: "图片")
            }
            val opened = processImage {
                val picture = Mobilepanel.openPicture(bytes)
                val width = picture.width()
                val height = picture.height()
                var sampleSize = 1
                while (maxOf(width, height) / sampleSize > 2048) {
                    sampleSize *= 2
                }
                val options = BitmapFactory.Options().apply { inSampleSize = sampleSize }
                val thumbnail = BitmapFactory.decodeByteArray(bytes, 0, bytes.size, options)
                    ?: throw IOException("无法生成图片缩略图")
                EditorPicture(picture, thumbnail, width, height, picture.initialCrop(), name)
            }
            editor = opened
            showingPreview = false
            invalidatePreview()
        }
    }

    fun selectAlgorithm(index: Int) {
        if (busy || index == algorithm) return
        algorithm = index
        invalidatePreview()
    }

    fun setPreviewMode(enabled: Boolean) {
        if (busy || editor == null || enabled == previewMode) return
        showingPreview = enabled
        if (enabled) refreshPreview() else stopPreview()
    }

    fun transform(
        dx: Double,
        dy: Double,
        factor: Double,
        anchorX: Double,
        anchorY: Double,
        degrees: Double,
    ) {
        editCrop { it.crop.transform(dx, dy, factor, anchorX, anchorY, degrees) }
    }

    fun rotate(degrees: Double) {
        editCrop { it.crop.rotate(degrees) }
    }

    fun resetCrop() {
        editCrop { it.picture.initialCrop() }
    }

    private fun editCrop(change: (EditorPicture) -> Crop) {
        val current = editor ?: return
        if (busy) return
        editor = current.copy(crop = change(current))
        invalidatePreview()
    }

    fun editAddress(value: String) {
        if (busy || entry != EntryMode.GATE) return
        address = value
        identityNotice = null
    }

    /** 关掉当前会话并清空槽位；会话关闭后可重复调用。 */
    private fun closeSession() {
        session?.close()
        session = null
        sessionTarget = ""
    }

    /** 离线不建会话。取景、预览和导出可用，上传与相框操作不出现。 */
    fun enterOffline() {
        if (busy) return
        closeSession()
        bluetooth = null
        entry = EntryMode.OFFLINE
    }

    /** 关闭当前会话并回到开屏。已记住的地址保留，但不会自动重连。 */
    fun returnToGate() {
        if (busy) return
        closeSession()
        bluetooth = null
        entry = EntryMode.GATE
    }

    /** 先用新会话替换旧会话，保证切换过程中始终有一个可用句柄。 */
    private fun installSession(device: DeviceSession, target: String) {
        closeSession()
        session = device
        sessionTarget = target
    }

    fun clearBluetoothError() {
        bluetoothError = null
    }

    /** 先从 BLE 读取状态确认连接可用，再用这个会话替换当前连接。 */
    fun connectBluetooth(frame: NearbyFrame) {
        if (busy || bluetooth != null) return
        bluetoothError = null
        bluetoothConnecting = true
        runOperation(failure = "蓝牙连接失败", onFailure = { bluetoothError = it }) {
            var opened: DeviceSession? = null
            try {
                val connected = withContext(Dispatchers.IO) {
                    val device = Mobilepanel.connectBluetooth(
                        frame.name, frame.address, FrameBluetoothDialer(getApplication<Application>(), frame.device),
                    )
                    try {
                        device.status()
                        confirmFrameIdentity(device.id(), Mobilepanel.identityFromBluetooth(frame.address))
                    } catch (error: Exception) {
                        // 核对失败时本次会话不能留给其他页面使用。
                        device.close()
                        throw error
                    }
                    device
                }
                opened = connected
                installSession(connected, frame.address)
                bluetooth = FrameBluetoothSelection(frame.name, frame.address)
                entry = EntryMode.BLUETOOTH
                resetUploadDestination()
            } finally {
                opened?.takeIf { it !== session }?.close()
                bluetoothConnecting = false
            }
        }
    }

    fun disconnectBluetooth() {
        returnToGate()
    }

    /** 探到相框才进入工作区。探不到时留在开屏，不留下半开的会话。 */
    fun connectTypedWiFi() {
        if (busy || entry != EntryMode.GATE) return
        val target = address.trim()
        runOperation(failure = "没有连上相框", onFailure = { identityNotice = it }) {
            val device = withContext(Dispatchers.IO) {
                val opened = Mobilepanel.connectWiFi(target)
                try {
                    opened.status()
                    confirmFrameIdentity(opened.id(), frameIdentity)
                } catch (error: Exception) {
                    opened.close()
                    throw error
                }
                opened
            }
            installSession(device, target)
            entry = EntryMode.WIFI
            identityNotice = null
            rememberConnection(device)
        }
    }

    override fun onCleared() {
        closeSession()
        super.onCleared()
    }

    private fun resetUploadDestination() {
        manualDestination = false
        uploadDestination = null
    }

    fun selectUploadDestination(destination: FrameDestination) {
        if (busy) return
        manualDestination = true
        uploadDestination = destination
        reply = null
    }

    /** 地址变化后探测 SD 卡，手动选择优先于后台回执。会话已被替换时丢弃本次结果。 */
    suspend fun detectUploadDestination(device: DeviceSession?) {
        if (device == null || busy || manualDestination) return
        uploadDestination = null
        delay(350)
        if (manualDestination) return
        val sdAvailable = try {
            withContext(Dispatchers.IO) {
                val available = device.storage().sdAvailable()
                confirmFrameIdentity(device.id(), frameIdentity)
                available
            }
        } catch (error: CancellationException) {
            throw error
        } catch (error: Exception) {
            identityNotice = error.localizedMessage ?: "没能确认相框身份"
            false
        }
        currentCoroutineContext().ensureActive()
        if (device === session) identityNotice = null
        if (device === session && !manualDestination) {
            uploadDestination = if (sdAvailable) FrameDestination.SD else FrameDestination.FLASH
        }
    }

    /** 传图、浏览和固件升级共用上次成功的 Wi-Fi 地址，蓝牙选择只在本次运行有效。 */
    fun rememberConnection(device: DeviceSession) {
        if (bluetooth != null || device !== session) return
        val target = address.trim()
        if (target.isNotEmpty()) preferences.edit().putString("frame_address", target).apply()
    }

    /**
     * 两侧身份都有且不同时拒绝连接。旧固件不报 id，或蓝牙地址推不出基 MAC 时放行。
     * 确认成功后记住这台相框；Wi-Fi 地址只在这次走的是手填地址时更新。
     */
    /** 图库刷新时核对 Wi-Fi 相框。核对失败只留下提示，不打断已经读到的列表。 */
    fun confirmWiFiIdentity(device: DeviceSession, known: String) {
        val notice = try {
            confirmFrameIdentity(device.id(), known)
            null
        } catch (error: Exception) {
            error.localizedMessage ?: "没能确认相框身份"
        }
        viewModelScope.launch {
            // 核对期间换了相框就丢弃旧结果，避免提示指向另一台设备。
            if (device === session) identityNotice = notice
        }
    }

    private fun confirmFrameIdentity(reported: String, known: String) {
        if (reported.isNotEmpty() && known.isNotEmpty() && reported != known) {
            throw IOException("连上的是另一台相框（$reported），已记住的是 $known")
        }
        if (reported.isEmpty()) return
        frameIdentity = reported
        preferences.edit().putString("frame_identity", reported).apply()
    }

    fun sendPicture() {
        val snapshot = editor ?: return
        val destination = uploadDestination ?: return
        val preset = algorithm
        val device = session ?: return
        runOperation(upload = true, failure = "没能发送画面", onFailure = { reply = FrameUploadReply(it, error = true) }) {
            val rendered = render(snapshot, preset)
            val frame = processImage { rendered.frame() }
            val response = coroutineScope {
                val updates = MutableStateFlow<FrameUploadProgress?>(null)
                val collector = launch { updates.collect { uploadProgress = it } }
                try {
                    withContext(Dispatchers.IO) {
                        device.send(
                            frame, destination.source, snapshot.name,
                            object : FrameTransferListener {
                                override fun onFrameProgress(stage: String, percent: Int, message: String) {
                                    updates.value = FrameUploadProgress(stage, percent, message)
                                }
                            },
                        )
                    }
                } finally {
                    collector.cancel()
                    uploadProgress = updates.value
                }
            }
            rememberConnection(device)
            reply = FrameUploadReply(response)
        }
    }

    fun exportPicture(uri: Uri) {
        val snapshot = editor ?: return
        val preset = algorithm
        runOperation(failure = "没能导出帧文件") {
            val rendered = render(snapshot, preset)
            val frame = processImage { rendered.frame() }
            withContext(Dispatchers.IO) {
                contentResolver.openOutputStream(uri, "wt")?.use { it.write(frame) }
                    ?: throw IOException("无法写入选中的文件")
            }
            message = "已导出 ${frame.size} 字节的帧文件"
        }
    }

    fun selectFirmware(uri: Uri) {
        runOperation(failure = "没能读取固件", onFailure = ::firmwareError) {
            firmware = null
            firmwareFeedback = FirmwareFeedback("正在读取固件…")
            firmware = withContext(Dispatchers.IO) {
                val name = contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)
                    ?.use { if (it.moveToFirst()) it.getString(0) else null }
                    ?: "已选择的固件"
                // 按应用槽上限读取一个额外字节，以识别过大的文件，避免把任意文件全部读进内存。
                val buffer = ByteArray(0x180000 + 1)
                val length = contentResolver.openInputStream(uri)?.use { input ->
                    var offset = 0
                    while (offset < buffer.size) {
                        val count = input.read(buffer, offset, buffer.size - offset)
                        if (count < 0) break
                        offset += count
                    }
                    offset
                } ?: throw IOException("无法读取选中的固件")
                if (length == buffer.size) throw IOException("固件超过 1.5 MiB 应用槽容量")
                val bytes = buffer.copyOf(length)
                SelectedFirmware(name, Mobilepanel.inspectFirmware(bytes), bytes)
            }
            firmwareFeedback = null
        }
    }

    /** ViewModel 持有升级任务，配置变化时重用同一事务，避免重新上传或丢失确认。 */
    fun upgradeFirmware() {
        val selected = firmware ?: return
        val device = session ?: return
        runOperation(failure = "固件升级未确认", onFailure = ::firmwareError) {
            coroutineScope {
                val updates = MutableStateFlow(FirmwareFeedback("正在查询相框…"))
                firmwareFeedback = updates.value
                val collector = launch { updates.collect { firmwareFeedback = it } }
                try {
                    withContext(Dispatchers.IO) {
                        device.upgrade(selected.data, object : OTAListener {
                            override fun onProgress(message: String, sent: Long, total: Long) {
                                val fraction = if (total > 0) (sent.toFloat() / total).coerceIn(0f, 1f) else null
                                updates.value = FirmwareFeedback(message, fraction)
                            }
                        })
                    }
                } finally {
                    collector.cancel()
                }
            }
            rememberConnection(device)
            firmware = null
            firmwareFeedback = FirmwareFeedback("固件 ${selected.version} 已启动并确认。")
        }
    }

    private fun firmwareError(text: String) {
        firmwareFeedback = FirmwareFeedback(text, error = true)
    }

    private fun runOperation(
        upload: Boolean = false,
        failure: String,
        onFailure: (String) -> Unit = { message = it },
        action: suspend () -> Unit,
    ) {
        if (busy) return
        stopPreview()
        busy = true
        sending = upload
        uploadProgress = null
        reply = null
        message = null
        viewModelScope.launch {
            try {
                action()
            } catch (error: CancellationException) {
                throw error
            } catch (error: Exception) {
                onFailure("$failure：${error.localizedMessage.orEmpty()}")
            } finally {
                busy = false
                sending = false
                refreshPreview()
            }
        }
    }

    private fun invalidatePreview() {
        stopPreview()
        cachedRender = null
        preview = null
        reply = null
        message = null
        refreshPreview()
    }

    private fun stopPreview() {
        previewJob?.cancel()
        previewJob = null
        previewing = false
    }

    /** 预览协程持有单次请求，取消后仍可能等待 Go 调用结束，只有当前协程可提交结果。 */
    private fun refreshPreview() {
        val snapshot = editor ?: return
        if (!previewMode || busy || preview != null || previewJob != null) return
        val preset = algorithm
        val job = viewModelScope.launch(start = CoroutineStart.LAZY) {
            try {
                delay(120)
                val rendered = render(snapshot, preset)
                val bitmap = processImage {
                    val png = rendered.preview()
                    BitmapFactory.decodeByteArray(png, 0, png.size)
                        ?: throw IOException("无法读取六色预览")
                }
                if (previewJob === coroutineContext.job) {
                    preview = bitmap
                }
            } catch (error: CancellationException) {
                throw error
            } catch (error: Exception) {
                if (previewJob === coroutineContext.job) {
                    message = "没能生成预览：${error.localizedMessage.orEmpty()}"
                }
            } finally {
                if (previewJob === coroutineContext.job) {
                    previewJob = null
                    previewing = false
                }
            }
        }
        previewJob = job
        previewing = true
        job.start()
    }

    private suspend fun render(snapshot: EditorPicture, preset: Int): Rendered {
        cachedRender?.let { cache ->
            if (cache.editor === snapshot && cache.algorithm == preset) return cache.rendered
        }
        val rendered = processImage { snapshot.picture.render(snapshot.crop, preset) }
        cachedRender = RenderCache(snapshot, preset, rendered)
        return rendered
    }

    /** Go 调用不可协作取消，用同一把锁限制并发的解码、取样、抖动与编码。 */
    private suspend fun <T> processImage(action: () -> T): T = withContext(Dispatchers.Default) {
        imageWork.withLock {
            ensureActive()
            action()
        }
    }
}
