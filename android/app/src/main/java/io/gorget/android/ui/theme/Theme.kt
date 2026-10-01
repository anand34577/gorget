package io.gorget.android.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.ExperimentalTextApi
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontVariation
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import io.gorget.android.R

/** Tempered-steel palette shared with the web console. */
@Immutable
data class Steel(
    val bg: Color,
    val surface: Color,
    val surface2: Color,
    val sunken: Color,
    val line: Color,
    val lineStrong: Color,
    val ink: Color,
    val ink2: Color,
    val ink3: Color,
    val blued: Color,
    val bluedSoft: Color,
    val verdigris: Color,
    val verdigrisSoft: Color,
    val straw: Color,
    val strawSoft: Color,
    val oxide: Color,
    val oxideSoft: Color,
)

val TemperGradient = listOf(Color(0xFF3A55B4), Color(0xFF7B4FA8), Color(0xFFC49A3A))

private val LightSteel = Steel(
    bg = Color(0xFFEDF0F3), surface = Color.White, surface2 = Color(0xFFF6F8FA), sunken = Color(0xFFE3E8ED),
    line = Color(0xFFD5DCE3), lineStrong = Color(0xFFB8C2CC), ink = Color(0xFF141A22), ink2 = Color(0xFF4A5663), ink3 = Color(0xFF7A8693),
    blued = Color(0xFF3A55B4), bluedSoft = Color(0xFFE3E8F8), verdigris = Color(0xFF2E8C7E), verdigrisSoft = Color(0xFFDCEFEB),
    straw = Color(0xFFA87F22), strawSoft = Color(0xFFF5ECD6), oxide = Color(0xFFB5473A), oxideSoft = Color(0xFFF6E1DE),
)

private val DarkSteel = Steel(
    bg = Color(0xFF0F1318), surface = Color(0xFF161B22), surface2 = Color(0xFF1B212A), sunken = Color(0xFF0B0E12),
    line = Color(0xFF262E39), lineStrong = Color(0xFF364150), ink = Color(0xFFE6EBF0), ink2 = Color(0xFFA3AEBA), ink3 = Color(0xFF6F7B88),
    blued = Color(0xFF6F87E0), bluedSoft = Color(0xFF1F2846), verdigris = Color(0xFF4FB7A6), verdigrisSoft = Color(0xFF15302C),
    straw = Color(0xFFD9B45A), strawSoft = Color(0xFF33291A), oxide = Color(0xFFE0705F), oxideSoft = Color(0xFF3A1D19),
)

val LocalSteel = staticCompositionLocalOf { LightSteel }

val PlexSans = FontFamily(
    Font(R.font.plex_sans_regular, FontWeight.Normal),
    Font(R.font.plex_sans_medium, FontWeight.Medium),
    Font(R.font.plex_sans_semibold, FontWeight.SemiBold),
)
val PlexMono = FontFamily(Font(R.font.plex_mono_regular))

@OptIn(ExperimentalTextApi::class)
val Bricolage = FontFamily(
    Font(R.font.bricolage, FontWeight.SemiBold, variationSettings = FontVariation.Settings(FontVariation.weight(620), FontVariation.width(85f))),
    Font(R.font.bricolage, FontWeight.Bold, variationSettings = FontVariation.Settings(FontVariation.weight(700), FontVariation.width(85f))),
)

private val Type = Typography(
    displaySmall = TextStyle(fontFamily = Bricolage, fontWeight = FontWeight.SemiBold, fontSize = 32.sp, lineHeight = 36.sp, letterSpacing = (-0.5).sp),
    headlineSmall = TextStyle(fontFamily = Bricolage, fontWeight = FontWeight.SemiBold, fontSize = 24.sp, lineHeight = 28.sp, letterSpacing = (-0.3).sp),
    titleLarge = TextStyle(fontFamily = Bricolage, fontWeight = FontWeight.SemiBold, fontSize = 21.sp, lineHeight = 26.sp),
    titleMedium = TextStyle(fontFamily = PlexSans, fontWeight = FontWeight.SemiBold, fontSize = 16.sp, lineHeight = 22.sp),
    titleSmall = TextStyle(fontFamily = PlexSans, fontWeight = FontWeight.Medium, fontSize = 14.sp, lineHeight = 20.sp),
    bodyLarge = TextStyle(fontFamily = PlexSans, fontSize = 16.sp, lineHeight = 23.sp),
    bodyMedium = TextStyle(fontFamily = PlexSans, fontSize = 14.sp, lineHeight = 20.sp),
    bodySmall = TextStyle(fontFamily = PlexSans, fontSize = 12.sp, lineHeight = 17.sp),
    labelLarge = TextStyle(fontFamily = PlexSans, fontWeight = FontWeight.Medium, fontSize = 14.sp),
    labelMedium = TextStyle(fontFamily = PlexSans, fontWeight = FontWeight.Medium, fontSize = 12.sp),
    labelSmall = TextStyle(fontFamily = PlexSans, fontWeight = FontWeight.Medium, fontSize = 11.sp, letterSpacing = 0.6.sp),
)

val MonoStyle = TextStyle(fontFamily = PlexMono, fontSize = 13.sp)

@Composable
fun GorgetTheme(themePref: String = "system", content: @Composable () -> Unit) {
    val dark = when (themePref) {
        "dark" -> true
        "light" -> false
        else -> isSystemInDarkTheme()
    }
    val s = if (dark) DarkSteel else LightSteel
    val scheme = if (dark) {
        darkColorScheme(
            primary = s.blued, onPrimary = Color(0xFF0C1020), primaryContainer = s.bluedSoft, onPrimaryContainer = s.ink,
            secondary = s.verdigris, background = s.bg, onBackground = s.ink, surface = s.surface, onSurface = s.ink,
            surfaceVariant = s.surface2, onSurfaceVariant = s.ink2, outline = s.lineStrong, outlineVariant = s.line,
            error = s.oxide, errorContainer = s.oxideSoft, surfaceContainer = s.surface, surfaceContainerHigh = s.surface2,
            surfaceContainerLow = s.surface, surfaceContainerHighest = s.surface2, surfaceContainerLowest = s.sunken,
        )
    } else {
        lightColorScheme(
            primary = s.blued, onPrimary = Color.White, primaryContainer = s.bluedSoft, onPrimaryContainer = s.ink,
            secondary = s.verdigris, background = s.bg, onBackground = s.ink, surface = s.surface, onSurface = s.ink,
            surfaceVariant = s.surface2, onSurfaceVariant = s.ink2, outline = s.lineStrong, outlineVariant = s.line,
            error = s.oxide, errorContainer = s.oxideSoft, surfaceContainer = s.surface, surfaceContainerHigh = s.surface2,
            surfaceContainerLow = s.surface, surfaceContainerHighest = s.surface2, surfaceContainerLowest = s.sunken,
        )
    }
    androidx.compose.runtime.CompositionLocalProvider(LocalSteel provides s) {
        MaterialTheme(colorScheme = scheme, typography = Type, content = content)
    }
}
