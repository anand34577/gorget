package io.gorget.android.ui

import android.content.Intent
import android.net.Uri
import androidx.browser.customtabs.CustomTabsIntent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import io.gorget.android.core.GorgetCore
import io.gorget.android.core.Status
import io.gorget.android.ui.theme.LocalSteel
import io.gorget.android.ui.theme.MonoStyle
import kotlinx.coroutines.launch

/** Dark steel header with the lames, shared by the sign-in screens. */
@Composable
private fun BrandHeader(title: String, subtitle: String) {
    Box(
        Modifier
            .fillMaxWidth()
            .background(Color(0xFF10151C))
            .statusBarsPadding()
            .padding(horizontal = 28.dp, vertical = 28.dp),
    ) {
        Column(verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Lames(LameState.ON, size = 76.dp)
            Text(title, style = MaterialTheme.typography.displaySmall, color = Color(0xFFE6EBF0))
            Text(subtitle, style = MaterialTheme.typography.bodyMedium, color = Color(0xFFA3AEBA))
        }
    }
}

@Composable
fun OnboardingScreen(onDone: () -> Unit = {}) {
    val s = LocalSteel.current
    val scope = rememberCoroutineScope()
    var url by rememberSaveable { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    val submit = {
        if (!busy && url.isNotBlank()) {
            busy = true
            error = null
            scope.launch {
                runCatching { GorgetCore.setServer(url) }
                    .onSuccess { onDone() }
                    .onFailure { error = it.message ?: "Couldn't reach that server." }
                busy = false
            }
        }
    }
    Column(Modifier.fillMaxSize().background(s.bg).verticalScroll(rememberScrollState()).imePadding()) {
        BrandHeader("Join your network", "Gorget connects your devices privately. Nothing leaves your own server.")
        Column(Modifier.padding(24.dp).widthIn(max = 560.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            OutlinedTextField(
                value = url,
                onValueChange = { url = it.trim() },
                label = { Text("Server address") },
                placeholder = { Text("vpn.example.com") },
                singleLine = true,
                textStyle = MonoStyle,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go, autoCorrectEnabled = false),
                keyboardActions = KeyboardActions(onGo = { submit() }),
                isError = error != null,
                supportingText = { Text(error ?: "Ask your administrator if you don't know it.") },
                modifier = Modifier.fillMaxWidth(),
            )
            Button(onClick = { submit() }, enabled = !busy && url.isNotBlank(), modifier = Modifier.fillMaxWidth().height(52.dp)) {
                if (busy) CircularProgressIndicator(Modifier.height(20.dp), strokeWidth = 2.dp, color = Color.White) else Text("Continue")
            }
        }
    }
}

@Composable
fun LoginScreen(status: Status) {
    val s = LocalSteel.current
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var showKey by rememberSaveable { mutableStateOf(false) }
    var key by rememberSaveable { mutableStateOf("") }
    val openUrl = { u: String ->
        runCatching { CustomTabsIntent.Builder().setShowTitle(true).build().launchUrl(ctx, Uri.parse(u)) }
            .onFailure { ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(u))) }
    }
    val network = status.networkName.ifBlank { status.serverUrl.removePrefix("https://") }

    Column(Modifier.fillMaxSize().background(s.bg).verticalScroll(rememberScrollState()).imePadding().navigationBarsPadding()) {
        BrandHeader(
            if (status.state == Status.STATE_EXPIRED) "Sign in again" else "Sign in",
            "to $network",
        )
        Column(Modifier.padding(24.dp).widthIn(max = 560.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            (error ?: status.error)?.let { Banner(it, Tone.DANGER) }
            val code = status.loginCode
            val loginUrl = status.loginUrl
            if (code != null && loginUrl != null) {
                Panel(Modifier.fillMaxWidth()) {
                    Column(Modifier.padding(18.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                        Text("Finish signing in in your browser", style = MaterialTheme.typography.titleMedium)
                        Text("Check that the browser shows this code:", style = MaterialTheme.typography.bodyMedium, color = s.ink2)
                        Text(code, style = MaterialTheme.typography.headlineSmall.copy(fontFamily = io.gorget.android.ui.theme.PlexMono), color = s.ink)
                        Row2 {
                            CircularProgressIndicator(Modifier.height(18.dp), strokeWidth = 2.dp)
                            Text("Waiting for approval…", style = MaterialTheme.typography.bodySmall, color = s.ink3)
                        }
                        OutlinedButton(onClick = { openUrl(loginUrl) }, modifier = Modifier.fillMaxWidth()) { Text("Open the sign-in page again") }
                    }
                }
            } else {
                Button(
                    onClick = {
                        busy = true
                        error = null
                        scope.launch {
                            runCatching { GorgetCore.startBrowserLogin() }
                                .onSuccess { openUrl(it.url) }
                                .onFailure { error = it.message }
                            busy = false
                        }
                    },
                    enabled = !busy,
                    modifier = Modifier.fillMaxWidth().height(52.dp),
                ) { Text("Sign in with browser") }
            }
            if (!showKey) {
                OutlinedButton(onClick = { showKey = true }, modifier = Modifier.fillMaxWidth().height(48.dp)) { Text("Use a setup key instead") }
            } else {
                OutlinedTextField(
                    value = key, onValueChange = { key = it.trim() }, label = { Text("Setup key") }, singleLine = true, textStyle = MonoStyle,
                    supportingText = { Text("Starts with gsk_. Created by an admin under Setup keys.") },
                    modifier = Modifier.fillMaxWidth(),
                )
                Button(
                    onClick = {
                        busy = true
                        error = null
                        scope.launch {
                            runCatching { GorgetCore.loginWithSetupKey(key) }.onFailure { error = it.message }
                            busy = false
                        }
                    },
                    enabled = !busy && key.startsWith("gsk_"),
                    colors = ButtonDefaults.buttonColors(),
                    modifier = Modifier.fillMaxWidth().height(48.dp),
                ) { Text("Join with setup key") }
            }
            Spacer(Modifier.height(8.dp))
            TextButton(onClick = { ServerReset.request() }, modifier = Modifier.align(Alignment.CenterHorizontally)) {
                Text("Use a different server", color = s.ink2)
            }
        }
    }
}

@Composable
private fun Row2(content: @Composable () -> Unit) {
    androidx.compose.foundation.layout.Row(horizontalArrangement = Arrangement.spacedBy(10.dp), verticalAlignment = Alignment.CenterVertically) { content() }
}

/** Lets the login screen ask the root to show the server step again. */
object ServerReset {
    val requested = mutableStateOf(false)
    fun request() {
        requested.value = true
    }
}
