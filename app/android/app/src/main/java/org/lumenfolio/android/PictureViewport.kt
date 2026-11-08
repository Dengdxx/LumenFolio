package org.lumenfolio.android

import android.graphics.Bitmap
import android.graphics.Paint
import android.graphics.RectF
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectTransformGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.drawIntoCanvas
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.unit.dp

@Composable
internal fun PictureViewport(
    picture: EditorPicture,
    previewMode: Boolean,
    preview: Bitmap?,
    previewing: Boolean,
    frameWidth: Int,
    frameHeight: Int,
    enabled: Boolean,
    onTransform: (Double, Double, Double, Double, Double, Double) -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier.aspectRatio(frameWidth.toFloat() / frameHeight).clip(RoundedCornerShape(8.dp)),
        contentAlignment = Alignment.Center,
    ) {
        if (previewMode) {
            if (preview != null) {
                val image = remember(preview) { preview.asImageBitmap() }
                Image(
                    image,
                    contentDescription = "六色预览",
                    modifier = Modifier.matchParentSize(),
                    contentScale = ContentScale.Fit,
                )
            } else {
                Box(
                    Modifier.matchParentSize().background(MaterialTheme.colorScheme.surfaceVariant),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(if (previewing) "正在生成六色预览…" else "六色预览不可用")
                }
            }
        } else {
            CropCanvas(picture, frameWidth, frameHeight, enabled, onTransform, Modifier.matchParentSize())
        }
    }
}

/**
 * 触点和画布使用同一面板坐标系。显示用缩略图，输出仍按原图尺寸采样。
 */
@Composable
private fun CropCanvas(
    picture: EditorPicture,
    frameWidth: Int,
    frameHeight: Int,
    enabled: Boolean,
    onTransform: (Double, Double, Double, Double, Double, Double) -> Unit,
    modifier: Modifier,
) {
    val paint = remember { Paint(Paint.FILTER_BITMAP_FLAG) }
    val bounds = remember { RectF() }
    val gestures = if (enabled) {
        Modifier.pointerInput(picture.bitmap) {
            detectTransformGestures { centroid, pan, zoom, rotation ->
                val panelX = frameWidth.toDouble() / size.width
                val panelY = frameHeight.toDouble() / size.height
                onTransform(
                    pan.x * panelX,
                    pan.y * panelY,
                    zoom.toDouble(),
                    centroid.x * panelX,
                    centroid.y * panelY,
                    rotation.toDouble(),
                )
            }
        }
    } else {
        Modifier
    }

    Canvas(modifier.background(Color.White).then(gestures)) {
        val crop = picture.crop
        val scaleX = size.width / frameWidth
        val scaleY = size.height / frameHeight
        val halfWidth = (picture.width * crop.scale() / 2).toFloat()
        val halfHeight = (picture.height * crop.scale() / 2).toFloat()
        bounds.set(-halfWidth, -halfHeight, halfWidth, halfHeight)
        drawIntoCanvas { canvas ->
            val native = canvas.nativeCanvas
            val saved = native.save()
            native.translate(
                size.width / 2 + crop.offsetX().toFloat() * scaleX,
                size.height / 2 + crop.offsetY().toFloat() * scaleY,
            )
            native.scale(scaleX, scaleY)
            native.rotate(crop.rotation().toFloat())
            native.drawBitmap(picture.bitmap, null, bounds, paint)
            native.restoreToCount(saved)
        }
    }
}
