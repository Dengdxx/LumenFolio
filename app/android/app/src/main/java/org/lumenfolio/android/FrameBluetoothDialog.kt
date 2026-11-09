package org.lumenfolio.android

import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

@Composable
internal fun FrameBluetoothDialog(model: FrameUploadViewModel, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var scanJob by remember { mutableStateOf<Job?>(null) }
    var frames by remember { mutableStateOf(listOf<NearbyFrame>()) }
    var scanFeedback by remember { mutableStateOf<String?>(null) }
    var scanning by remember { mutableStateOf(false) }

    fun scan() {
        model.clearBluetoothError()
        frames = emptyList()
        scanFeedback = null
        scanning = true
        scanJob = scope.launch {
            try {
                withTimeoutOrNull(10_000) {
                    scanNearbyFrames(context).collect { frame ->
                        frames = (frames.filterNot { it.address == frame.address } + frame).sortedBy { it.address }
                    }
                }
                if (frames.isEmpty()) scanFeedback = "未发现相框。长按相框重刷键后再试。"
            } catch (error: CancellationException) {
                throw error
            } catch (error: Exception) {
                scanFeedback = error.localizedMessage ?: "蓝牙扫描失败"
            } finally {
                scanning = false
            }
        }
    }

    val permissions = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { grants ->
        if (frameBluetoothPermissions.all { grants[it] == true }) {
            scan()
        } else {
            scanFeedback = "请允许蓝牙扫描所需的系统权限。"
        }
    }
    val selection = model.bluetooth
    val connecting = model.bluetoothConnecting
    AlertDialog(
        onDismissRequest = { if (!connecting) onDismiss() },
        title = { Text("蓝牙连接") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                if (selection != null) {
                    Text("已选择 ${selection.name}")
                    Text(selection.address, style = MaterialTheme.typography.bodySmall)
                } else {
                    Text("长按相框重刷键约 2 秒进入蓝牙连接模式，然后搜索附近的相框。")
                    if (connecting || scanning) {
                        LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                        Text(if (connecting) "正在连接相框…" else "正在搜索相框…")
                    }
                    Column(
                        modifier = Modifier.heightIn(max = 200.dp).verticalScroll(rememberScrollState()),
                        verticalArrangement = Arrangement.spacedBy(6.dp),
                    ) {
                        for (frame in frames) {
                            OutlinedButton(
                                onClick = {
                                    scanJob?.cancel()
                                    scanFeedback = null
                                    model.connectBluetooth(frame)
                                },
                                enabled = !connecting,
                                modifier = Modifier.fillMaxWidth(),
                            ) {
                                Column {
                                    Text(frame.name)
                                    Text(frame.address, style = MaterialTheme.typography.bodySmall)
                                }
                            }
                        }
                    }
                    (model.bluetoothError ?: scanFeedback)?.let {
                        Text(it, color = MaterialTheme.colorScheme.error)
                    }
                }
            }
        },
        confirmButton = {
            if (selection != null) {
                TextButton(onClick = onDismiss) { Text("完成") }
            } else {
                TextButton(
                    enabled = !scanning && !connecting,
                    onClick = {
                        val required = frameBluetoothPermissions
                        if (required.all { context.checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED }) {
                            scan()
                        } else {
                            permissions.launch(required)
                        }
                    },
                ) { Text("搜索相框") }
            }
        },
        dismissButton = {
            if (selection != null) {
                TextButton(onClick = {
                    model.disconnectBluetooth()
                    frames = emptyList()
                }) { Text("断开蓝牙") }
            } else {
                TextButton(onClick = onDismiss, enabled = !connecting) { Text("取消") }
            }
        },
    )
}
