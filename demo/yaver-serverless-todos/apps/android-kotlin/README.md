# Todo Android Kotlin — Yaver Serverless

Native Android Todo app backed by the Yaver Serverless Lite data API. It uses
programmatic Android views and `HttpURLConnection` so it remains a small WebRTC
remote-runtime fixture.

```bash
cd demo/yaver-serverless-todos/apps/android-kotlin
gradle wrapper --gradle-version 8.10.2
./gradlew assembleDebug
```

Set the backend URL, project slug, and `pp_` token in the app.
