# gomobile bindings are called from native code via JNI.
-keep class go.** { *; }
-keep class io.gorget.gorgetcore.** { *; }
-keep interface io.gorget.gorgetcore.** { *; }

# kotlinx.serialization
-keepattributes *Annotation*, InnerClasses
-keepclassmembers class io.gorget.android.core.** {
    *** Companion;
    kotlinx.serialization.KSerializer serializer(...);
}
-keep,includedescriptorclasses class io.gorget.android.core.**$$serializer { *; }
