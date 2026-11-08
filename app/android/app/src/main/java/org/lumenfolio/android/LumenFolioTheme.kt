package org.lumenfolio.android

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

private val DarkColors = darkColorScheme(
    primary = Color(0xFF0A84FF),
    onPrimary = Color(0xFF141413),
    onPrimaryContainer = Color(0xFF8CC8FF),
    secondary = Color(0xFFFF9F0A),
    secondaryContainer = Color(0xFF12324D),
    onSecondaryContainer = Color(0xFF8CC8FF),
    tertiary = Color(0xFF30D158),
    background = Color(0xFF0A0A0A),
    onBackground = Color(0xFFF5F5F7),
    surface = Color(0xFF1C1C1E),
    surfaceContainer = Color(0xFF2C2C2E),
    onSurface = Color(0xFFF5F5F7),
    surfaceVariant = Color(0xFF2C2C2E),
    onSurfaceVariant = Color(0xFF98989D),
    outline = Color(0x2EFFFFFF),
    outlineVariant = Color(0x1AFFFFFF),
    error = Color(0xFFFF6961),
)

private val LightColors = lightColorScheme(
    primary = Color(0xFFD07044),
    onPrimary = Color(0xFF141413),
    onPrimaryContainer = Color(0xFF8C401D),
    secondary = Color(0xFF85550B),
    secondaryContainer = Color(0xFFF4E2D9),
    onSecondaryContainer = Color(0xFF8C401D),
    tertiary = Color(0xFF297A41),
    background = Color(0xFFF7F6F0),
    onBackground = Color(0xFF141413),
    surface = Color(0xFFFAFAF7),
    surfaceContainer = Color(0xFFF0EFE8),
    onSurface = Color(0xFF141413),
    surfaceVariant = Color(0xFFF0EFE8),
    onSurfaceVariant = Color(0xFF3D3C3A),
    outline = Color(0x381F1E1D),
    outlineVariant = Color(0x1F1F1E1D),
    error = Color(0xFFB83131),
)

@Composable
internal fun LumenFolioTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = if (isSystemInDarkTheme()) DarkColors else LightColors,
        content = content,
    )
}
