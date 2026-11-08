package org.lumenfolio.android

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels

class MainActivity : ComponentActivity() {
    private val model: FrameUploadViewModel by viewModels()

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        if (savedInstanceState == null && intent.action == Intent.ACTION_VIEW) {
            intent.data?.let(model::openImage)
        }
        setContent {
            LumenFolioTheme {
                FrameUploadScreen(model)
            }
        }
    }
}
