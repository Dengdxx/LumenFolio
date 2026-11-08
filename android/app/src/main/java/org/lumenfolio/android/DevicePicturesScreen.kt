package org.lumenfolio.android

import android.graphics.BitmapFactory
import androidx.compose.foundation.Image
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
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.FilterChip
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
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
import kotlinx.coroutines.withContext
import org.lumenfolio.mobilepanel.Mobilepanel

private enum class PictureSource(val id: String, val label: String) {
    ARTIFACTS("artifacts", "Flash artifacts"),
    SD("sd", "SD 卡"),
}

private data class DevicePicture(val id: String, val name: String)
private data class PictureListRequest(val host: String, val source: PictureSource, val refresh: Int)

private sealed interface PictureRead<out T> {
    data object Loading : PictureRead<Nothing>
    data class Success<T>(val value: T) : PictureRead<T>
    data class Failure(val message: String) : PictureRead<Nothing>
}

@Composable
internal fun DevicePicturesScreen(
    address: String,
    onAddressChange: (String) -> Unit,
    onConnected: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val focusManager = LocalFocusManager.current
    var host by remember { mutableStateOf(address.trim()) }
    var source by rememberSaveable { mutableStateOf(PictureSource.ARTIFACTS) }
    var refresh by remember { mutableStateOf(0) }
    val request = PictureListRequest(host, source, refresh)
    var selected by remember(request) { mutableStateOf<DevicePicture?>(null) }
    val result = if (request.host.isNotEmpty()) {
        readPicture(request, onSuccess = { onConnected(request.host) }) {
            val list = Mobilepanel.listDevicePictures(request.host, request.source.id)
            List(list.count()) { index -> DevicePicture(list.id(index), list.name(index)) }
        }
    } else {
        null
    }

    Box(modifier, contentAlignment = Alignment.TopCenter) {
        LazyColumn(
            modifier = Modifier.widthIn(max = 560.dp).fillMaxSize(),
            contentPadding = PaddingValues(horizontal = 16.dp, vertical = 20.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            item {
                Panel {
                    Text("相框图片", style = MaterialTheme.typography.titleMedium)
                    FrameAddressField(address, onAddressChange = { value ->
                        onAddressChange(value)
                        host = ""
                    })
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
                    Button(
                        onClick = {
                            focusManager.clearFocus()
                            host = address.trim()
                            refresh++
                        },
                        enabled = address.isNotBlank() && result != PictureRead.Loading,
                        modifier = Modifier.fillMaxWidth(),
                    ) {
                        Text(if (host.isEmpty()) "读取列表" else "刷新列表")
                    }
                }
            }

            when (result) {
                null -> item { PictureMessage("输入相框地址后读取图片。") }
                PictureRead.Loading -> item { PictureLoading("正在读取${source.label}图片…") }
                is PictureRead.Failure -> item { PictureMessage("读取列表失败：${result.message}", error = true) }
                is PictureRead.Success -> {
                    if (result.value.isEmpty()) item { PictureMessage("${source.label}中没有图片。") }
                    items(result.value, key = { it.id }) { picture ->
                        ListItem(
                            headlineContent = {
                                Text(picture.name, maxLines = 2, overflow = TextOverflow.Ellipsis)
                            },
                            trailingContent = { Text("预览") },
                            modifier = Modifier.clickable { selected = picture },
                        )
                    }
                }
            }
        }
    }

    selected?.let { picture -> DevicePicturePreview(request, picture) { selected = null } }
}

@Composable
private fun DevicePicturePreview(request: PictureListRequest, picture: DevicePicture, onDismiss: () -> Unit) {
    val result = readPicture(picture.id) {
        val png = Mobilepanel.previewDevicePicture(request.host, request.source.id, picture.id)
        BitmapFactory.decodeByteArray(png, 0, png.size)
            ?: throw IOException("无法解码相框图片")
    }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(picture.name, maxLines = 2, overflow = TextOverflow.Ellipsis) },
        text = {
            when (result) {
                PictureRead.Loading -> PictureLoading("正在读取图片…")
                is PictureRead.Failure -> PictureMessage("读取图片失败：${result.message}", error = true)
                is PictureRead.Success -> Image(
                    bitmap = result.value.asImageBitmap(),
                    contentDescription = picture.name,
                    modifier = Modifier.fillMaxWidth().heightIn(max = 480.dp),
                )
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text("关闭") } },
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
