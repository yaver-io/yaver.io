package io.yaver.plainssh;

import com.facebook.react.ReactPackage;
import com.facebook.react.bridge.*;
import com.facebook.react.uimanager.ViewManager;
import java.util.*;
import java.util.concurrent.*;
import plainssh.Plainssh;

public final class YaverPlainSSHPackage implements ReactPackage {
  public List<NativeModule> createNativeModules(ReactApplicationContext context) {
    return Collections.singletonList(new Bridge(context));
  }
  public List<ViewManager> createViewManagers(ReactApplicationContext context) {
    return Collections.emptyList();
  }
  private static final class Bridge extends ReactContextBaseJavaModule {
    private final ExecutorService work = Executors.newFixedThreadPool(4);
    Bridge(ReactApplicationContext context) { super(context); }
    public String getName() { return "YaverPlainSSH"; }
    @ReactMethod public void invoke(String request, Promise promise) {
      work.execute(() -> {
        try { promise.resolve(Plainssh.invoke(request)); }
        catch (Exception error) { promise.reject("SSH_NATIVE_FAILED", "SSH operation failed. Reconnect to the host."); }
      });
    }
    @Override public void invalidate() { Plainssh.invoke("{\"op\":\"closeAll\"}"); work.shutdownNow(); super.invalidate(); }
  }
}
