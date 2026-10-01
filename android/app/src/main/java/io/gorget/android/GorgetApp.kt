package io.gorget.android

import android.app.Application
import io.gorget.android.core.GorgetCore
import io.gorget.android.notify.Notifier

class GorgetApp : Application() {
    override fun onCreate() {
        super.onCreate()
        GorgetCore.init(this)
        Notifier.createChannel(this)
    }
}
