package io.yaver.tv

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.json.JSONObject

class VibingCatalogTest {
    @Test
    fun renderedAppsStayAndBackendOnlyRowsDisappear() {
        val rows = listOf(
            ProjectRow("root", "/root"),
            ProjectRow("api", "/work/api", framework = "go", surfaces = listOf("backend")),
            ProjectRow("sfmg", "/work/sfmg", framework = "expo", surfaces = listOf("mobile", "web")),
            ProjectRow("storefront", "/work/storefront", framework = "nextjs", surfaces = listOf("web")),
            ProjectRow("workspace", "/work/workspace", framework = "monorepo", frameworks = listOf("go", "flutter"), surfaces = listOf("backend", "mobile"), isMonorepo = true),
        )

        assertEquals(listOf("sfmg", "storefront", "workspace"), filteredVibingProjects(rows, "").map { it.name })
        assertEquals(listOf("sfmg"), filteredVibingProjects(rows, "expo").map { it.name })
        assertEquals(listOf("Mobile", "Web"), vibingCapabilityLabels(rows[2]))
        assertEquals(listOf("Web", "Frontend"), vibingCapabilityLabels(rows[3]))
        assertEquals("flutter", vibingDisplayFramework(rows[4]))
    }

    @Test
    fun readyStatusMustBelongToSelectedCheckout() {
        assertTrue(devServerOwnsProject("/root/Workspace/sfmg/", "/root/Workspace/sfmg"))
        assertFalse(devServerOwnsProject("/root/Workspace/yaver.io/mobile", "/root/Workspace/sfmg"))
        assertFalse(devServerOwnsProject(null, "/root/Workspace/sfmg"))
    }

    @Test
    fun relativePreviewUrlsResolveOnTheRenderBoxAgent() {
        assertEquals(
            "http://127.0.0.1:18080/dev-web/",
            vibingCaptureTarget(
                JSONObject().put("directUrl", "http://10.0.0.5:8081"),
                JSONObject().put("webUrl", "/dev-web/"),
            ),
        )
        assertEquals(
            "http://127.0.0.1:18080/dev/",
            vibingCaptureTarget(JSONObject().put("bundleUrl", "/dev/")),
        )
    }
}
