package org.lumenfolio.android

import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.FilterChip
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import java.io.IOException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.lumenfolio.mobilepanel.Mobilepanel

/** Go 读取返回前始终占用一个传输名额，即使列表已切换或退出。 */
private val thumbnailTransfer = Mutex()

private enum class PictureSource(val id: String, val label: String) {
    ARTIFACTS("artifacts", "Flash artifacts"),
    SD("sd", "SD 卡"),
}

private data class DevicePicture(val id: String, val name: String, val revision: String)
private data class PictureListRequest(val host: String, val source: PictureSource, val refresh: Int)
private data class DevicePicturePage(val pictures: List<DevicePicture>, val next: String)

/** 当前请求的 ID 用于列表去重，revision 用于区分同名画面内容的变化。 */
private fun readDevicePicturePage(request: PictureListRequest, after: String): DevicePicturePage {
    val page = Mobilepanel.listDevicePicturePage(request.host, request.source.id, after)
    val pictures = List(page.count()) { index -> DevicePicture(page.id(index), page.name(index), page.revision(index)) }
    return DevicePicturePage(pictures.distinctBy { it.id }, page.next())
}

private sealed interface PictureRead<out T> {
    data object Loading : PictureRead<Nothing>
    data class Success<T>(val value: T) : PictureRead<T>
    data class Failure(val message: String) : PictureRead<Nothing>
}

@Composable
internal fun DevicePicturesScreen(
    address: String,
    bluetooth: FrameBluetoothSelection?,
    onAddressChange: (String) -> Unit,
    onConnected: (String) -> Unit,
    onBluetooth: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val focusManager = LocalFocusManager.current
    val connectionHost = bluetooth?.host ?: address.trim()
    var host by remember(bluetooth?.host) { mutableStateOf(connectionHost) }
    var source by rememberSaveable { mutableStateOf(PictureSource.ARTIFACTS) }
    var listRefresh by remember { mutableStateOf(0) }
    var infoRefresh by remember { mutableStateOf(0) }
    val request = PictureListRequest(host, source, listRefresh)
    var selected by remember(request) { mutableStateOf<DevicePicture?>(null) }
    val listState = rememberLazyListState()
    val thumbnails = remember(request) { mutableStateMapOf<DevicePicture, ImageBitmap?>() }
    val result = if (request.host.isNotEmpty()) {
        readPicture(request, onSuccess = { onConnected(request.host) }) {
            readDevicePicturePage(request, "")
        }
    } else {
        null
    }
    val firstPage = if (result is PictureRead.Success) result.value else null
    var page by remember(request, firstPage) { mutableStateOf(firstPage) }
    val pictures = page?.pictures.orEmpty()
    val next = page?.next.orEmpty()
    var loadingMore by remember(request, next, selected) { mutableStateOf(false) }
    var loadMoreError by remember(request) { mutableStateOf<String?>(null) }
    if (next.isNotEmpty() && selected == null && loadMoreError == null) {
        LaunchedEffect(request, next) {
            val tail = pictures.takeLast(2).mapTo(mutableSetOf<Any>()) { it.id }
            snapshotFlow { tail.isEmpty() || listState.layoutInfo.visibleItemsInfo.any { it.key in tail } }.first { it }
            loadingMore = true
            try {
                val added = withContext(Dispatchers.IO) { readDevicePicturePage(request, next) }
                page = DevicePicturePage((pictures + added.pictures).associateBy { it.id }.values.toList(), added.next)
            } catch (error: CancellationException) {
                throw error
            } catch (error: Exception) {
                loadMoreError = error.localizedMessage ?: "请确认相框已唤醒且地址正确。"
            } finally {
                loadingMore = false
            }
        }
    }
    val infoRequest = host to infoRefresh
    val storage = if (host.isNotEmpty()) {
        readPicture(infoRequest) {
            val capacity = Mobilepanel.deviceStorage(infoRequest.first)
            capacity.flash() to capacity.sd()
        }
    } else {
        null
    }
    val (flashCapacity, sdCapacity) = when (storage) {
        null -> "待读取容量" to "待读取容量"
        PictureRead.Loading -> "正在读取容量…" to "正在读取容量…"
        is PictureRead.Success -> storage.value
        is PictureRead.Failure -> "容量不可用" to "容量不可用"
    }
    val status = if (host.isNotEmpty()) {
        readPicture(infoRequest) { Mobilepanel.deviceStatus(infoRequest.first) }
    } else {
        null
    }

    if (result is PictureRead.Success && selected == null) {
        LaunchedEffect(request, pictures) {
            val indices = pictures.withIndex().associate { it.value.id to it.index }
            snapshotFlow {
                listState.layoutInfo.visibleItemsInfo.mapNotNull { item -> (item.key as? String)?.let(indices::get) }
            }.collectLatest { visible ->
                if (visible.isEmpty()) {
                    thumbnails.clear()
                    return@collectLatest
                }
                val after = visible.last() + 1
                val end = minOf(pictures.size, after + 2)
                // 解码结果仅留在视口周边，滚回较远的条目时由 Go 缓存提供小图。
                thumbnails.keys.retainAll(pictures.subList(maxOf(0, visible.first() - 2), end).toSet())
                for (picture in visible.map(pictures::get) + pictures.subList(after, end)) {
                    if (thumbnails.containsKey(picture)) continue
                    thumbnails[picture] = try {
                        thumbnailTransfer.withLock {
                            withContext(Dispatchers.IO) {
                                val png = Mobilepanel.thumbnailDevicePictureFor(
                                    request.host, request.source.id, picture.id, picture.revision,
                                )
                                BitmapFactory.decodeByteArray(png, 0, png.size)?.asImageBitmap()
                            }
                        }
                    } catch (error: CancellationException) {
                        throw error
                    } catch (_: Exception) {
                        null
                    }
                }
            }
        }
    }

    Box(modifier, contentAlignment = Alignment.TopCenter) {
        LazyColumn(
            modifier = Modifier.widthIn(max = 560.dp).fillMaxSize(),
            state = listState,
            contentPadding = PaddingValues(horizontal = 16.dp, vertical = 20.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            item {
                Panel {
                    Text("相框图片", style = MaterialTheme.typography.titleMedium)
                    PictureMessage(
                        when (status) {
                            null -> "待读取相框状态"
                            PictureRead.Loading -> "正在读取相框状态…"
                            is PictureRead.Success -> status.value
                            is PictureRead.Failure -> "相框状态不可用"
                        },
                    )
                    FrameConnectionField(
                        address = address,
                        bluetooth = bluetooth,
                        onAddressChange = { value ->
                            onAddressChange(value)
                            host = ""
                        },
                        onBluetooth = onBluetooth,
                    )
                    Row(
                        modifier = Modifier.horizontalScroll(rememberScrollState()),
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        for (option in PictureSource.entries) {
                            FilterChip(
                                selected = source == option,
                                onClick = { source = option },
                                label = { Text(option.label) },
                            )
                        }
                    }
                    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                        PictureMessage("Flash artifacts $flashCapacity")
                        PictureMessage("SD 卡 $sdCapacity")
                    }
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        Button(
                            onClick = {
                                focusManager.clearFocus()
                                host = connectionHost
                                listRefresh++
                            },
                            enabled = connectionHost.isNotEmpty() && result != PictureRead.Loading,
                            modifier = Modifier.weight(1f),
                        ) {
                            Text(if (host.isEmpty()) "读取列表" else "刷新列表")
                        }
                        if (host.isNotEmpty()) {
                            OutlinedButton(
                                onClick = { infoRefresh++ },
                                enabled = storage != PictureRead.Loading && status != PictureRead.Loading,
                                modifier = Modifier.weight(1f),
                            ) {
                                Text("刷新状态")
                            }
                        }
                    }
                }
            }

            when (result) {
                null -> item { PictureMessage("输入相框地址或连接蓝牙后读取图片。") }
                PictureRead.Loading -> item { PictureLoading("正在读取${source.label}图片…") }
                is PictureRead.Failure -> item { PictureMessage("读取列表失败：${result.message}", error = true) }
                is PictureRead.Success -> {
                    if (pictures.isEmpty() && next.isEmpty()) item { PictureMessage("${source.label}中没有图片。") }
                    items(pictures, key = { it.id }) { picture ->
                        ListItem(
                            leadingContent = {
                                Box(
                                    modifier = Modifier.size(54.dp, 72.dp)
                                        .clip(RoundedCornerShape(6.dp))
                                        .background(MaterialTheme.colorScheme.surfaceVariant),
                                    contentAlignment = Alignment.Center,
                                ) {
                                    val thumbnail = thumbnails[picture]
                                    if (thumbnail != null) {
                                        Image(thumbnail, contentDescription = null, modifier = Modifier.fillMaxSize())
                                    } else {
                                        Text(
                                            if (thumbnails.containsKey(picture)) "—" else "…",
                                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        )
                                    }
                                }
                            },
                            headlineContent = {
                                Text(picture.name, maxLines = 2, overflow = TextOverflow.Ellipsis)
                            },
                            trailingContent = { Text("预览") },
                            modifier = Modifier.clickable { selected = picture },
                        )
                    }
                    if (loadingMore) item { PictureLoading("正在读取下一页…") }
                    loadMoreError?.let { message ->
                        item {
                            Column {
                                PictureMessage("读取下一页失败：$message", error = true)
                                TextButton(onClick = { loadMoreError = null }) { Text("重试") }
                            }
                        }
                    }
                }
            }
        }
    }

    selected?.let { picture ->
        DevicePicturePreview(
            request,
            picture,
            onDismiss = { selected = null },
            onDeleted = {
                selected = null
                listRefresh++
                infoRefresh++
            },
        )
    }
}

@Composable
private fun DevicePicturePreview(
    request: PictureListRequest,
    picture: DevicePicture,
    onDismiss: () -> Unit,
    onDeleted: () -> Unit,
) {
    val scope = rememberCoroutineScope()
    var display by remember(request, picture.id) { mutableStateOf<PictureRead<Unit>?>(null) }
    val canDelete = remember(request.source, picture.id) {
        Mobilepanel.canDeleteDevicePicture(request.source.id, picture.id)
    }
    var confirmingDelete by remember(request, picture.id) { mutableStateOf(false) }
    var deleting by remember(request, picture.id) { mutableStateOf(false) }
    var deleteError by remember(request, picture.id) { mutableStateOf<String?>(null) }
    val deleteColors = ButtonDefaults.textButtonColors(contentColor = MaterialTheme.colorScheme.error)
    val result = readPicture(picture.id) {
        val png = Mobilepanel.previewDevicePicture(request.host, request.source.id, picture.id)
        BitmapFactory.decodeByteArray(png, 0, png.size)
            ?: throw IOException("无法解码相框图片")
    }

    if (confirmingDelete) {
        AlertDialog(
            onDismissRequest = { if (!deleting) confirmingDelete = false },
            title = { Text("确认删除图片") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(picture.name)
                    Text("来源 ${request.source.label}")
                    if (deleting) PictureLoading("正在删除图片…")
                    deleteError?.let { PictureMessage("删除失败，$it", error = true) }
                }
            },
            confirmButton = {
                TextButton(
                    enabled = !deleting,
                    colors = deleteColors,
                    onClick = {
                        if (!deleting) {
                            deleting = true
                            deleteError = null
                            scope.launch {
                                try {
                                    withContext(Dispatchers.IO) {
                                        Mobilepanel.deleteDevicePicture(request.host, request.source.id, picture.id)
                                    }
                                } catch (error: CancellationException) {
                                    throw error
                                } catch (error: Exception) {
                                    deleteError = error.localizedMessage ?: "请确认相框已唤醒且地址正确。"
                                    return@launch
                                } finally {
                                    deleting = false
                                }
                                onDeleted()
                            }
                        }
                    },
                ) { Text("删除") }
            },
            dismissButton = {
                TextButton(onClick = { confirmingDelete = false }, enabled = !deleting) { Text("取消") }
            },
        )
        return
    }

    AlertDialog(
        onDismissRequest = { if (display != PictureRead.Loading) onDismiss() },
        title = { Text(picture.name, maxLines = 2, overflow = TextOverflow.Ellipsis) },
        text = {
            Column(
                modifier = Modifier.verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                when (result) {
                    PictureRead.Loading -> PictureLoading("正在读取图片…")
                    is PictureRead.Failure -> PictureMessage("读取图片失败：${result.message}", error = true)
                    is PictureRead.Success -> Image(
                        bitmap = result.value.asImageBitmap(),
                        contentDescription = picture.name,
                        modifier = Modifier.fillMaxWidth().heightIn(max = 480.dp),
                    )
                }
                when (val state = display) {
                    null -> Unit
                    PictureRead.Loading -> PictureLoading("正在提交显示请求…")
                    is PictureRead.Success -> PictureMessage("已安排显示，屏幕刷新约需 35 秒。")
                    is PictureRead.Failure -> PictureMessage("显示请求失败：${state.message}", error = true)
                }
            }
        },
        confirmButton = {
            TextButton(
                enabled = display == null || display is PictureRead.Failure,
                onClick = {
                    if (display == null || display is PictureRead.Failure) {
                        display = PictureRead.Loading
                        scope.launch {
                            display = try {
                                withContext(Dispatchers.IO) {
                                    Mobilepanel.displayDevicePicture(request.host, request.source.id, picture.id)
                                }
                                PictureRead.Success(Unit)
                            } catch (error: CancellationException) {
                                throw error
                            } catch (error: Exception) {
                                PictureRead.Failure(error.localizedMessage ?: "请确认相框已唤醒且地址正确。")
                            }
                        }
                    }
                },
            ) { Text("在相框上显示") }
        },
        dismissButton = {
            Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                if (canDelete) {
                    TextButton(
                        enabled = display != PictureRead.Loading,
                        colors = deleteColors,
                        onClick = {
                            if (display != PictureRead.Loading) {
                                deleteError = null
                                confirmingDelete = true
                            }
                        },
                    ) { Text("删除") }
                }
                TextButton(onClick = onDismiss, enabled = display != PictureRead.Loading) { Text("关闭") }
            }
        },
    )
}

@Composable
private fun PictureLoading(message: String) {
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        PictureMessage(message)
        LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
    }
}

@Composable
private fun PictureMessage(message: String, error: Boolean = false) {
    val colors = MaterialTheme.colorScheme
    Text(
        message,
        color = if (error) colors.error else colors.onSurfaceVariant,
        style = MaterialTheme.typography.bodyMedium,
        modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
    )
}

/** 每次读取持有独立的结果状态。切换来源、地址或预览时，旧的 Go 调用完成后也不能覆盖当前结果。 */
@Composable
private fun <T> readPicture(
    request: Any,
    onSuccess: () -> Unit = {},
    read: () -> T,
): PictureRead<T> {
    var result by remember(request) { mutableStateOf<PictureRead<T>>(PictureRead.Loading) }
    LaunchedEffect(request) {
        result = try {
            val value = withContext(Dispatchers.IO) { read() }
            onSuccess()
            PictureRead.Success(value)
        } catch (error: CancellationException) {
            throw error
        } catch (error: Exception) {
            PictureRead.Failure(error.localizedMessage ?: "请确认相框已唤醒且地址正确。")
        }
    }
    return result
}
