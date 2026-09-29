package io.yaver.tv

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class RunnerPreferenceContractTest {
    @Test
    fun convexArrayHydratesEveryCodingPreference() {
        val rows = JSONArray().put(JSONObject()
            .put("deviceId", "box-1")
            .put("runnerId", "codex")
            .put("model", "gpt-5.6-sol")
            .put("reasoningEffort", "max"))
            .put(JSONObject()
                .put("deviceId", "box-2")
                .put("runnerId", "opencode")
                .put("model", "deepseek/deepseek-flash")
                .put("provider", "deepseek")
                .put("mode", "build"))

        val parsed = parseRunnerPreferenceMaps(rows)
        assertEquals("codex", parsed.runners["box-1"])
        assertEquals("gpt-5.6-sol", parsed.models["box-1"])
        assertEquals("max", parsed.reasoningEfforts["box-1"])
        assertEquals("deepseek", parsed.providers["box-2"])
        assertEquals("build", parsed.modes["box-2"])
    }

    @Test
    fun settingsWriteUsesTheConvexSingleDevicePatchShape() {
        val body = runnerPreferenceSettingsPatch(
            deviceId = "box-1",
            runnerId = "opencode",
            model = "deepseek/deepseek-flash",
            reasoningEffort = null,
            provider = "deepseek",
        )
        val row = body.getJSONObject("primaryRunnerForDevice")
        assertEquals("box-1", row.getString("deviceId"))
        assertEquals("opencode", row.getString("runnerId"))
        assertEquals("deepseek", row.getString("provider"))
        assertEquals("deepseek/deepseek-flash", row.getString("model"))
        assertFalse(row.has("box-1"))
    }
}
