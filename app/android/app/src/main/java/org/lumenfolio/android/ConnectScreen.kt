package org.lumenfolio.android

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.colorResource
import androidx.compose.ui.res.painterResource
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

/** 开屏连接页。进入工作区之前必须先选一种方式，记住的地址不会自动连接。 */
@Composable
internal fun ConnectScreen(model: FrameUploadViewModel, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var step by remember { mutableStateOf("") }
    var frames by remember { mutableStateOf(listOf<NearbyFrame>()) }
    var scanning by remember { mutableStateOf(false) }
    var scanJob by remember { mutableStateOf<Job?>(null) }
    var feedback by remember { mutableStateOf<String?>(null) }

    fun scan() {
        frames = emptyList()
        feedback = null
        scanning = true
        scanJob = scope.launch {
            try {
                withTimeoutOrNull(8_000) {
                    scanNearbyFrames(context).collect { frame ->
                        val found = (frames.filterNot { it.address == frame.address } + frame).sortedBy { it.address }
                        frames = found
                        // 只有一台相框在广播时直接连，不再让用户点一次。
                        if (found.size == 1) model.connectBluetooth(found.first())
                    }
                }
                if (frames.isEmpty() && model.entry == EntryMode.GATE) {
                    feedback = "没有找到相框。长按相框重刷键约两秒后再试。"
                }
            } catch (error: CancellationException) {
                throw error
            } catch (error: Exception) {
                feedback = error.localizedMessage ?: "蓝牙扫描失败"
            } finally {
                scanning = false
            }
        }
    }

    val permissions = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { grants ->
        if (frameBluetoothPermissions.all { grants[it] == true }) scan() else feedback = "请允许蓝牙扫描所需的系统权限。"
    }
    LaunchedEffect(model.entry) {
        if (model.entry != EntryMode.GATE) scanJob?.cancel()
    }

    Column(
        modifier = modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(24.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(Modifier.widthIn(max = 420.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
                Image(
                    painter = painterResource(R.drawable.lumenfolio_icon),
                    contentDescription = null,
                    modifier = Modifier.size(64.dp).background(
                        colorResource(R.color.lumenfolio_icon_background),
                        RoundedCornerShape(14.dp),
                    ).padding(4.dp),
                )
                Image(
                    painter = painterResource(R.drawable.lumenfolio_wordmark),
                    contentDescription = "LumenFolio",
                    modifier = Modifier.weight(1f).height(48.dp),
                    alignment = Alignment.CenterStart,
                    contentScale = ContentScale.Fit,
                    colorFilter = ColorFilter.tint(MaterialTheme.colorScheme.onSurface),
                )
            }
            OutlinedButton(onClick = { step = "wifi" }, enabled = !model.busy, modifier = Modifier.fillMaxWidth()) {
                Text("Wi-Fi")
            }
            OutlinedButton(
                onClick = {
                    step = "bluetooth"
                    if (frameBluetoothPermissions.all { context.checkSelfPermission(it) == android.content.pm.PackageManager.PERMISSION_GRANTED }) {
                        scan()
                    } else {
                        permissions.launch(frameBluetoothPermissions)
                    }
                },
                enabled = !model.busy,
                modifier = Modifier.fillMaxWidth(),
            ) { Text("蓝牙") }
            OutlinedButton(onClick = model::enterOffline, enabled = !model.busy, modifier = Modifier.fillMaxWidth()) {
                Text("离线")
            }
            if (step == "wifi") {
                FrameAddressField(model.address, model::editAddress, enabled = !model.busy)
                Button(
                    onClick = model::connectTypedWiFi,
                    enabled = !model.busy && model.address.isNotBlank(),
                    modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp),
                ) { Text(if (model.busy) "正在连接…" else "连接") }
            }
            if (step == "bluetooth") {
                if (scanning || model.bluetoothConnecting) LinearProgressIndicator(Modifier.fillMaxWidth())
                Text(
                    when {
                        model.bluetoothConnecting -> "正在连接相框…"
                        scanning -> "正在查找相框。只有一台时会直接连接。"
                        frames.size > 1 -> "找到多台相框，请选择一台。"
                        else -> feedback ?: "长按相框重刷键约两秒，让它开始广播。"
                    },
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    style = MaterialTheme.typography.bodySmall,
                )
                frames.takeIf { it.size > 1 }?.forEach { frame ->
                    OutlinedButton(
                        onClick = { model.connectBluetooth(frame) },
                        enabled = !model.busy,
                        modifier = Modifier.fillMaxWidth(),
                    ) { Text("${frame.name} · ${frame.address}") }
                }
            }
            (model.identityNotice ?: model.bluetoothError)?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}
