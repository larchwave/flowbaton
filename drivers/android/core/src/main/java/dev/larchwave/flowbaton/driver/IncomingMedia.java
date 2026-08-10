package dev.larchwave.flowbaton.driver;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.UncheckedIOException;
import java.nio.file.Files;
import java.nio.file.Path;

/** A bounded disk spool for one client-streamed media item. */
final class IncomingMedia implements AutoCloseable {
    private final long maximumBytes;
    private final Path path;
    private OutputStream output;
    private long size;

    IncomingMedia(long maximumBytes) {
        if (maximumBytes < 1) {
            throw new IllegalArgumentException("maximumBytes must be positive");
        }
        this.maximumBytes = maximumBytes;
        try {
            path = Files.createTempFile("flowbaton-media-", ".upload");
            output = Files.newOutputStream(path);
        } catch (IOException error) {
            throw new UncheckedIOException("creating the media spool", error);
        }
    }

    void append(byte[] chunk) throws IOException {
        if (chunk.length > maximumBytes - size) {
            throw new IllegalArgumentException(
                    "media payload exceeds the " + maximumBytes + "-byte ceiling");
        }
        output.write(chunk);
        size += chunk.length;
    }

    InputStream openInputStream() throws IOException {
        if (output != null) {
            output.close();
            output = null;
        }
        return Files.newInputStream(path);
    }

    Path path() {
        return path;
    }

    @Override
    public void close() throws IOException {
        IOException failure = null;
        if (output != null) {
            try {
                output.close();
            } catch (IOException error) {
                failure = error;
            }
            output = null;
        }
        try {
            Files.deleteIfExists(path);
        } catch (IOException error) {
            if (failure == null) {
                failure = error;
            } else {
                failure.addSuppressed(error);
            }
        }
        if (failure != null) {
            throw failure;
        }
    }
}
