package io.gorget.android.core

import android.content.Context

/** Android-only preferences (the Go core keeps network preferences). */
class AppPrefs(ctx: Context) {
    private val sp = ctx.getSharedPreferences("gorget_app", Context.MODE_PRIVATE)

    enum class SplitMode { ALL, ONLY_SELECTED, EXCEPT_SELECTED }

    var splitMode: SplitMode
        get() = runCatching { SplitMode.valueOf(sp.getString("split_mode", SplitMode.ALL.name)!!) }.getOrDefault(SplitMode.ALL)
        set(v) = sp.edit().putString("split_mode", v.name).apply()

    var splitApps: Set<String>
        get() = sp.getStringSet("split_apps", emptySet())!!.toSet()
        set(v) = sp.edit().putStringSet("split_apps", v).apply()

    var notifications: Boolean
        get() = sp.getBoolean("notifications", true)
        set(v) = sp.edit().putBoolean("notifications", v).apply()

    var theme: String
        get() = sp.getString("theme", "system")!!
        set(v) = sp.edit().putString("theme", v).apply()
}
