package io.yaver.feedback;

import android.app.Activity;
import android.content.Context;
import android.content.SharedPreferences;
import android.os.Handler;
import android.os.Looper;
import android.util.Log;

import androidx.annotation.NonNull;

import com.facebook.react.bridge.Promise;
import com.facebook.react.bridge.ReactApplicationContext;
import com.facebook.react.bridge.ReactContextBaseJavaModule;
import com.facebook.react.bridge.ReactMethod;
import com.facebook.react.bridge.ReadableMap;
import com.facebook.react.bridge.WritableMap;
import com.facebook.react.bridge.Arguments;
import com.facebook.react.ReactApplication;
import com.facebook.react.ReactInstanceManager;

import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.concurrent.Executors;

/**
 * Hot reload native module for the Yaver Feedback SDK (Android).
 *
 * Downloads a Hermes bytecode bundle from the agent, saves it to the app's
 * files directory, and recreates the React Native context to load the new bundle.
 *
 * Supports N reloads — each reload recreates the ReactContext with the updated bundle.
 */
public class YaverHotReloadModule extends ReactContextBaseJavaModule {

    private static final String TAG = "YaverHotReload";
    private static final String MODULE_NAME = "YaverHotReload";
    private static final String BUNDLE_DIR = "yaver-hot-reload";
    private static final String BUNDLE_FILE = "index.android.bundle";
    private static final String PREFS_NAME = "yaver_hot_reload";
    private static final String PREFS_KEY_BUNDLE = "bundle_path";

    public YaverHotReloadModule(ReactApplicationContext context) {
        super(context);
    }

    @Override
    @NonNull
    public String getName() {
        return MODULE_NAME;
    }

    /**
     * Download a Hermes bundle from the agent and trigger a bridge reload.
     */
    @ReactMethod
    public void loadBundle(String urlString, ReadableMap headers, Promise promise) {
        Executors.newSingleThreadExecutor().execute(() -> {
            try {
                URL url = new URL(urlString);
                HttpURLConnection conn = (HttpURLConnection) url.openConnection();
                conn.setConnectTimeout(60000);
                conn.setReadTimeout(60000);

                // Set auth headers
                if (headers != null) {
                    if (headers.hasKey("Authorization")) {
                        conn.setRequestProperty("Authorization", headers.getString("Authorization"));
                    }
                }

                int responseCode = conn.getResponseCode();
                if (responseCode != 200) {
                    promise.reject("HTTP_ERROR", "Status " + responseCode);
                    return;
                }

                InputStream is = conn.getInputStream();
                File dir = new File(getReactApplicationContext().getFilesDir(), BUNDLE_DIR);
                if (!dir.exists()) dir.mkdirs();
                File bundleFile = new File(dir, BUNDLE_FILE);

                FileOutputStream fos = new FileOutputStream(bundleFile);
                byte[] buffer = new byte[8192];
                int bytesRead;
                int totalBytes = 0;
                while ((bytesRead = is.read(buffer)) != -1) {
                    fos.write(buffer, 0, bytesRead);
                    totalBytes += bytesRead;
                }
                fos.close();
                is.close();
                conn.disconnect();

                Log.i(TAG, "saved " + totalBytes + " bytes to " + bundleFile.getAbsolutePath());

                // Validate Hermes bytecode (magic bytes at offset 4)
                if (totalBytes >= 12) {
                    java.io.RandomAccessFile raf = new java.io.RandomAccessFile(bundleFile, "r");
                    raf.seek(4);
                    int magic = Integer.reverseBytes(raf.readInt());
                    if (magic == 0x1F1903C1) {
                        int bcVersion = Integer.reverseBytes(raf.readInt());
                        Log.i(TAG, "Hermes bytecode BC" + bcVersion);
                    } else {
                        Log.w(TAG, "not Hermes bytecode (magic=0x" + Integer.toHexString(magic) + ")");
                    }
                    raf.close();
                }

                // Save bundle path to SharedPreferences for next app launch
                SharedPreferences prefs = getReactApplicationContext()
                        .getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE);
                prefs.edit().putString(PREFS_KEY_BUNDLE, bundleFile.getAbsolutePath()).apply();

                WritableMap result = Arguments.createMap();
                result.putBoolean("loaded", true);
                result.putInt("size", totalBytes);
                promise.resolve(result);

                // Reload the bridge on the main thread
                new Handler(Looper.getMainLooper()).post(() -> reloadBridge());

            } catch (Exception e) {
                Log.e(TAG, "download failed", e);
                promise.reject("DOWNLOAD_FAILED", e.getMessage(), e);
            }
        });
    }

    @ReactMethod
    public void hasBundle(Promise promise) {
        File bundleFile = getSavedBundleFile(getReactApplicationContext());
        promise.resolve(bundleFile != null && bundleFile.exists());
    }

    @ReactMethod
    public void clearBundle(Promise promise) {
        File dir = new File(getReactApplicationContext().getFilesDir(), BUNDLE_DIR);
        if (dir.exists()) {
            for (File f : dir.listFiles()) f.delete();
            dir.delete();
        }
        SharedPreferences prefs = getReactApplicationContext()
                .getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE);
        prefs.edit().remove(PREFS_KEY_BUNDLE).apply();
        promise.resolve(true);
    }

    /**
     * Recreate the React Native context with the new bundle.
     */
    private void reloadBridge() {
        Activity activity = getCurrentActivity();
        if (activity == null) {
            Log.e(TAG, "no current activity");
            return;
        }

        if (activity.getApplication() instanceof ReactApplication) {
            ReactApplication app = (ReactApplication) activity.getApplication();
            ReactInstanceManager manager = app.getReactNativeHost().getReactInstanceManager();
            Log.i(TAG, "recreating React context with new bundle");
            manager.recreateReactContextInBackground();
        } else {
            Log.e(TAG, "Application does not implement ReactApplication");
        }
    }

    // MARK: - Static helpers for Application/MainApplication

    /**
     * Returns the hot-reloaded bundle file if it exists.
     * Call from MainApplication.getJSBundleFile() to load the hot bundle on startup.
     */
    public static File getSavedBundleFile(Context context) {
        SharedPreferences prefs = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE);
        String path = prefs.getString(PREFS_KEY_BUNDLE, null);
        if (path != null) {
            File f = new File(path);
            if (f.exists()) return f;
        }
        return null;
    }
}
