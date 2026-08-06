package dev.larchwave.flowbaton

import android.content.ContentResolver
import android.content.ContentValues
import android.net.Uri
import android.os.Build
import android.provider.MediaStore

/** Lands a streamed addMedia payload in MediaStore, classified by extension. */
object MediaStoreWriter {
    fun write(resolver: ContentResolver, mediaName: String, mediaExt: String, data: ByteArray) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) {
            throw UnsupportedOperationException(
                "addMedia needs the API 29+ scoped MediaStore insert path",
            )
        }
        val ext = mediaExt.trimStart('.').lowercase()
        val mime = mimeFor(ext)
        val values =
            ContentValues().apply {
                put(
                    MediaStore.MediaColumns.DISPLAY_NAME,
                    if (mediaName.contains('.')) mediaName else "$mediaName.$ext",
                )
                put(MediaStore.MediaColumns.MIME_TYPE, mime)
                put(MediaStore.MediaColumns.IS_PENDING, 1)
            }
        val uri =
            resolver.insert(collectionFor(mime), values)
                ?: throw IllegalStateException("MediaStore refused the insert for $mediaName")
        val stream =
            resolver.openOutputStream(uri)
                ?: throw IllegalStateException("MediaStore returned no stream for $uri")
        stream.use { it.write(data) }
        values.clear()
        values.put(MediaStore.MediaColumns.IS_PENDING, 0)
        resolver.update(uri, values, null, null)
    }

    private fun mimeFor(ext: String): String =
        when (ext) {
            "png" -> "image/png"
            "jpg", "jpeg" -> "image/jpeg"
            "gif" -> "image/gif"
            "webp" -> "image/webp"
            "mp4" -> "video/mp4"
            "mov" -> "video/quicktime"
            else -> "application/octet-stream"
        }

    private fun collectionFor(mime: String): Uri =
        when {
            mime.startsWith("image/") -> MediaStore.Images.Media.EXTERNAL_CONTENT_URI
            mime.startsWith("video/") -> MediaStore.Video.Media.EXTERNAL_CONTENT_URI
            else -> MediaStore.Downloads.EXTERNAL_CONTENT_URI
        }
}
