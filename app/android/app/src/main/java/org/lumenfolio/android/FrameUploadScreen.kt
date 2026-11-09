package org.lumenfolio.android

import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.windowInsetsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedCard
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.res.colorResource
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import org.lumenfolio.mobilepanel.Mobilepanel

private val ControlShape = RoundedCornerShape(8.dp)

private enum class FramePage { EDITOR, PICTURES, FIRMWARE }

@Composable
internal fun FrameUploadScreen(model: FrameUploadViewModel) {
    val openImage = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        uri?.let(model::openImage)
    }
    val export = rememberLauncherForActivityResult(
        ActivityResultContracts.CreateDocument("application/octet-stream"),
    ) { uri ->
        uri?.let(model::exportPicture)
    }

    var page by rememberSaveable { mutableStateOf(FramePage.EDITOR) }
    var showingBluetooth by rememberSaveable { mutableStateOf(false) }
    val openBluetooth = { showingBluetooth = true }
    val editorScroll = rememberScrollState()
    BackHandler(enabled = page != FramePage.EDITOR) {
        if (!model.busy) page = FramePage.EDITOR
    }

    Scaffold(
        topBar = {
            LumenFolioTopBar(page != FramePage.EDITOR, !model.busy) {
                page = if (page == FramePage.EDITOR) FramePage.PICTURES else FramePage.EDITOR
            }
        },
    ) { insets ->
        val contentModifier = Modifier
            .fillMaxSize()
            .padding(insets)
            .consumeWindowInsets(insets)
            .imePadding()

        when (page) {
            FramePage.PICTURES -> DevicePicturesScreen(
                address = model.address,
                bluetooth = model.bluetooth,
                session = model.session,
                identity = model.frameIdentity,
                identityNotice = model.identityNotice,
                onAddressChange = model::editAddress,
                onConnected = model::rememberConnection,
                onIdentity = model::confirmWiFiIdentity,
                onBluetooth = openBluetooth,
                modifier = contentModifier,
            )
            FramePage.FIRMWARE -> FirmwareUpgradeScreen(model, openBluetooth, contentModifier)
            FramePage.EDITOR -> Box(
                modifier = contentModifier
                    .verticalScroll(editorScroll)
                    .padding(horizontal = 16.dp, vertical = 20.dp),
                contentAlignment = Alignment.TopCenter,
            ) {
                Column(
                    modifier = Modifier.widthIn(max = 560.dp).fillMaxWidth(),
                    verticalArrangement = Arrangement.spacedBy(16.dp),
                ) {
                    PicturePanel(model) { openImage.launch(arrayOf("image/png", "image/jpeg")) }
                    FrameUploadPanel(
                        model,
                        onExport = { export.launch("frame.bin") },
                        onUpgrade = { page = FramePage.FIRMWARE },
                        onBluetooth = openBluetooth,
                    )
                }
            }
        }
    }
    if (showingBluetooth) {
        FrameBluetoothDialog(model) {
            showingBluetooth = false
            model.clearBluetoothError()
        }
    }
}

@Composable
private fun LumenFolioTopBar(showingDevicePage: Boolean, enabled: Boolean, onSwitch: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    Surface(color = colors.surface) {
        Column {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .windowInsetsPadding(
                        WindowInsets.safeDrawing.only(WindowInsetsSides.Top + WindowInsetsSides.Horizontal),
                    )
                    .heightIn(min = 52.dp)
                    .padding(horizontal = 16.dp, vertical = 8.dp),
                horizontalArrangement = Arrangement.spacedBy(10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Image(
                    painter = painterResource(R.drawable.lumenfolio_icon),
                    contentDescription = null,
                    modifier = Modifier
                        .size(30.dp)
                        .background(colorResource(R.color.lumenfolio_icon_background), ControlShape)
                        .padding(3.dp),
                )
                Image(
                    painter = painterResource(R.drawable.lumenfolio_wordmark),
                    contentDescription = "LumenFolio",
                    modifier = Modifier.weight(1f).height(24.dp),
                    alignment = Alignment.CenterStart,
                    contentScale = ContentScale.Fit,
                    colorFilter = ColorFilter.tint(colors.onSurface),
                )
                TextButton(onClick = onSwitch, enabled = enabled) {
                    Text(if (showingDevicePage) "返回取景" else "相框图片")
                }
            }
            HorizontalDivider(color = colors.outlineVariant)
        }
    }
}

@Composable
private fun PicturePanel(model: FrameUploadViewModel, onOpen: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    val frameWidth = remember { Mobilepanel.width() }
    val frameHeight = remember { Mobilepanel.height() }

    Panel {
        Text("照片取景", style = MaterialTheme.typography.titleMedium)
        Text(
            "${frameWidth} × ${frameHeight} · 六色",
            color = colors.onSurfaceVariant,
            style = MaterialTheme.typography.bodyMedium,
        )
        OutlinedButton(
            onClick = onOpen,
            enabled = !model.busy,
            modifier = Modifier.fillMaxWidth(),
            shape = ControlShape,
        ) {
            Text("打开图片")
        }

        val picture = model.editor
        if (picture != null) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                FilterChip(
                    selected = !model.previewMode,
                    onClick = { model.setPreviewMode(false) },
                    enabled = !model.busy,
                    label = { Text("取景") },
                )
                FilterChip(
                    selected = model.previewMode,
                    onClick = { model.setPreviewMode(true) },
                    enabled = !model.busy,
                    label = { Text("六色预览") },
                )
            }
            Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                PictureViewport(
                    picture = picture,
                    previewMode = model.previewMode,
                    preview = model.preview,
                    previewing = model.previewing,
                    frameWidth = frameWidth,
                    frameHeight = frameHeight,
                    enabled = !model.busy,
                    onTransform = model::transform,
                    modifier = Modifier.widthIn(max = 360.dp).fillMaxWidth(),
                )
            }
            if (!model.previewMode) {
                Text(
                    "拖动平移，双指缩放或旋转。",
                    color = colors.onSurfaceVariant,
                    style = MaterialTheme.typography.bodySmall,
                )
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(onClick = { model.rotate(-90.0) }, enabled = !model.busy) { Text("−90°") }
                    OutlinedButton(onClick = { model.rotate(90.0) }, enabled = !model.busy) { Text("+90°") }
                    OutlinedButton(onClick = model::resetCrop, enabled = !model.busy) { Text("复位") }
                }
            }
            AlgorithmPicker(model.algorithm, model::selectAlgorithm, enabled = !model.busy)
            InkSwatches()
        }
    }
}

@Composable
private fun AlgorithmPicker(selected: Int, onSelect: (Int) -> Unit, enabled: Boolean) {
    val names = remember { List(Mobilepanel.algorithmCount(), Mobilepanel::algorithmName) }
    var expanded by remember { mutableStateOf(false) }
    Text("抖动算法", style = MaterialTheme.typography.labelLarge)
    Box {
        OutlinedButton(
            onClick = { expanded = true },
            enabled = enabled,
            modifier = Modifier.fillMaxWidth(),
            shape = ControlShape,
        ) {
            Text(names[selected] + " ▾")
        }
        DropdownMenu(expanded = expanded && enabled, onDismissRequest = { expanded = false }) {
            names.forEachIndexed { index, name ->
                DropdownMenuItem(
                    text = { Text(name) },
                    onClick = {
                        expanded = false
                        onSelect(index)
                    },
                )
            }
        }
    }
}

@Composable
private fun InkSwatches() {
    val colors = MaterialTheme.colorScheme
    val inks = remember {
        List(Mobilepanel.inkCount()) { index ->
            Mobilepanel.inkName(index) to Color(Mobilepanel.inkARGB(index))
        }
    }
    Row(
        Modifier.horizontalScroll(rememberScrollState()),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        for ((name, ink) in inks) {
            Row(horizontalArrangement = Arrangement.spacedBy(4.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(
                    Modifier.size(12.dp).background(ink, CircleShape).border(1.dp, colors.outline, CircleShape),
                )
                Text(name, color = colors.onSurfaceVariant, style = MaterialTheme.typography.labelSmall)
            }
        }
    }
}

@Composable
private fun FrameUploadPanel(
    model: FrameUploadViewModel,
    onExport: () -> Unit,
    onUpgrade: () -> Unit,
    onBluetooth: () -> Unit,
) {
    val colors = MaterialTheme.colorScheme
    val focusManager = LocalFocusManager.current
    val ready = model.editor != null && !model.busy
    val device = model.session
    val connected = device != null
    val connectionKey = model.bluetooth?.address ?: model.address.trim()
    val progress = model.uploadProgress
    val reply = model.reply
    // DeviceSession 的 equals 由 gobind 生成且恒为真，用连接标识而不是会话实例作为键。
    LaunchedEffect(connectionKey) { model.detectUploadDestination(device) }
    Panel {
        Row(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text("上传与导出", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
            UploadStatusBadge(model.sending, progress?.stage, reply)
        }
        Text(
            "使用同一局域网或蓝牙连接相框。相框休眠时，先按机身键唤醒。",
            color = colors.onSurfaceVariant,
            style = MaterialTheme.typography.bodyMedium,
        )
        FrameConnectionField(
            address = model.address,
            bluetooth = model.bluetooth,
            identity = model.frameIdentity,
            onAddressChange = model::editAddress,
            onBluetooth = onBluetooth,
            enabled = !model.busy,
        )
        model.identityNotice?.let {
            Text(it, color = colors.error, style = MaterialTheme.typography.bodySmall)
        }
        Text("存储位置", style = MaterialTheme.typography.labelLarge)
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            for (destination in FrameDestination.entries) {
                FilterChip(
                    selected = model.uploadDestination == destination,
                    onClick = { model.selectUploadDestination(destination) },
                    enabled = !model.busy && connected,
                    label = { Text(destination.label) },
                )
            }
        }
        if (connected && model.uploadDestination == null) {
            Text(
                "正在检测 SD 卡，也可以直接选择存储位置。",
                color = colors.onSurfaceVariant,
                style = MaterialTheme.typography.bodySmall,
            )
        }
        Button(
            onClick = {
                focusManager.clearFocus()
                model.sendPicture()
            },
            enabled = ready && connected && model.uploadDestination != null,
            shape = ControlShape,
            modifier = Modifier.fillMaxWidth().heightIn(min = 48.dp),
        ) {
            Text(if (model.sending) "正在处理…" else "上传到相框")
        }
        if (model.sending || (reply?.error == false && progress?.stage == "done")) {
            if (progress == null) {
                LinearProgressIndicator(modifier = Modifier.fillMaxWidth())
            } else {
                LinearProgressIndicator(progress = { progress.percent / 100f }, modifier = Modifier.fillMaxWidth())
            }
        }
        val feedback = model.message ?: reply?.message ?: if (model.sending) {
            progress?.message ?: "正在生成帧…"
        } else {
            null
        }
        if (feedback != null) {
            Text(
                feedback,
                modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
                color = if (reply?.error == true) colors.error else colors.onSurfaceVariant,
                style = MaterialTheme.typography.bodyMedium,
            )
        }
        OutlinedButton(
            onClick = {
                focusManager.clearFocus()
                onExport()
            },
            enabled = ready,
            shape = ControlShape,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text("导出帧文件")
        }
        OutlinedButton(
            onClick = {
                focusManager.clearFocus()
                onUpgrade()
            },
            enabled = !model.busy,
            shape = ControlShape,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text("固件升级")
        }
    }
}

@Composable
internal fun FrameConnectionField(
    address: String,
    bluetooth: FrameBluetoothSelection?,
    identity: String = "",
    onAddressChange: (String) -> Unit,
    onBluetooth: () -> Unit,
    enabled: Boolean = true,
) {
    if (bluetooth == null) {
        FrameAddressField(address, onAddressChange, enabled)
    } else {
        val label = buildString {
            append("蓝牙 · ${bluetooth.name}")
            if (identity.isNotEmpty()) append(" · $identity")
        }
        Text(label, style = MaterialTheme.typography.bodyMedium)
    }
    OutlinedButton(onClick = onBluetooth, enabled = enabled, modifier = Modifier.fillMaxWidth()) {
        Text(if (bluetooth == null) "蓝牙连接" else "管理蓝牙连接")
    }
}

@Composable
private fun FrameAddressField(address: String, onAddressChange: (String) -> Unit, enabled: Boolean) {
    val colors = MaterialTheme.colorScheme
    OutlinedTextField(
        value = address,
        onValueChange = onAddressChange,
        label = { Text("相框 IP 或主机名") },
        placeholder = { Text("192.168.1.42") },
        supportingText = { Text("端口 8080") },
        enabled = enabled,
        singleLine = true,
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Done),
        shape = ControlShape,
        colors = OutlinedTextFieldDefaults.colors(
            focusedContainerColor = colors.surfaceVariant,
            unfocusedContainerColor = colors.surfaceVariant,
            disabledContainerColor = colors.surfaceVariant,
            unfocusedBorderColor = colors.outline,
            focusedLabelColor = colors.onPrimaryContainer,
            disabledBorderColor = colors.outlineVariant,
        ),
        modifier = Modifier.fillMaxWidth(),
    )
}

@Composable
private fun UploadStatusBadge(sending: Boolean, stage: String?, reply: FrameUploadReply?) {
    val colors = MaterialTheme.colorScheme
    val (text, tint) = when {
        sending -> {
            val label = when (stage) {
                "upload" -> "正在上传"
                "storage" -> "正在保存"
                "queued" -> "等待写屏"
                "spi" -> "SPI 写屏"
                "done" -> "SPI 已写完"
                else -> "正在准备"
            }
            label to colors.onPrimaryContainer
        }
        reply?.error == true -> "发送异常" to colors.error
        reply != null && stage == "done" -> "SPI 已写完" to colors.tertiary
        reply != null -> "已回执" to colors.secondary
        else -> "待发送" to colors.onSurfaceVariant
    }
    Surface(color = tint.copy(alpha = 0.13f), contentColor = tint, shape = CircleShape) {
        Text(
            text,
            modifier = Modifier.padding(horizontal = 10.dp, vertical = 4.dp),
            style = MaterialTheme.typography.labelMedium,
        )
    }
}

@Composable
internal fun Panel(content: @Composable ColumnScope.() -> Unit) {
    val colors = MaterialTheme.colorScheme
    OutlinedCard(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(12.dp),
        colors = CardDefaults.outlinedCardColors(containerColor = colors.surface),
        border = BorderStroke(1.dp, colors.outlineVariant),
    ) {
        Column(
            modifier = Modifier.fillMaxWidth().padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
            content = content,
        )
    }
}
