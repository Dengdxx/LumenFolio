package org.lumenfolio.android

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp

@Composable
internal fun FirmwareUpgradeScreen(model: FrameUploadViewModel, modifier: Modifier = Modifier) {
    val focus = LocalFocusManager.current
    val choose = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        uri?.let(model::selectFirmware)
    }
    var confirming by rememberSaveable { mutableStateOf(false) }
    val selected = model.firmware
    val feedback = model.firmwareFeedback
    val host = model.address.trim()
    val canUpgrade = selected != null && host.isNotEmpty() && !model.busy

    Box(
        modifier = modifier.verticalScroll(rememberScrollState()).padding(horizontal = 16.dp, vertical = 20.dp),
        contentAlignment = Alignment.TopCenter,
    ) {
        Box(Modifier.widthIn(max = 560.dp).fillMaxWidth()) {
            Panel {
                Text("固件升级", style = MaterialTheme.typography.titleMedium)
                Text(
                    "将手机与相框接入同一局域网，唤醒相框后选择下载好的 LumenFolio 应用固件。",
                    style = MaterialTheme.typography.bodyMedium,
                )
                FrameAddressField(model.address, model::editAddress, enabled = !model.busy)
                OutlinedButton(
                    onClick = {
                        focus.clearFocus()
                        // 文件提供器可能没有为 .bin 声明 MIME，由 Go 检查实际镜像内容。
                        choose.launch(arrayOf("*/*"))
                    },
                    enabled = !model.busy,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text("选择固件 .bin")
                }
                if (selected != null) {
                    Text(selected.name, style = MaterialTheme.typography.bodyMedium)
                    Text("固件版本 ${selected.version}", style = MaterialTheme.typography.bodyMedium)
                }
                Button(
                    onClick = {
                        focus.clearFocus()
                        confirming = true
                    },
                    enabled = canUpgrade,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text("开始升级")
                }
                if (model.busy) {
                    val fraction = feedback?.fraction
                    if (fraction == null) {
                        LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
                    } else {
                        LinearProgressIndicator(progress = { fraction }, modifier = Modifier.fillMaxWidth())
                    }
                }
                if (feedback != null) {
                    val colors = MaterialTheme.colorScheme
                    Text(
                        feedback.message,
                        modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                        color = if (feedback.error) colors.error else colors.onSurfaceVariant,
                        style = MaterialTheme.typography.bodyMedium,
                    )
                }
            }
        }
    }

    if (confirming && selected != null && canUpgrade) {
        AlertDialog(
            onDismissRequest = { confirming = false },
            title = { Text("确认固件升级") },
            text = {
                Text("相框 $host\n固件 ${selected.name}\n版本 ${selected.version}\n\n升级期间请保持相框供电和网络连接，等待 App 确认重启后的固件。")
            },
            confirmButton = {
                TextButton(onClick = {
                    confirming = false
                    model.upgradeFirmware()
                }) { Text("确认升级") }
            },
            dismissButton = { TextButton(onClick = { confirming = false }) { Text("取消") } },
        )
    }
}
