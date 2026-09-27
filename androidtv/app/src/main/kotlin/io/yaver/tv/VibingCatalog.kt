package io.yaver.tv

import java.net.URI
import org.json.JSONObject

private val webFrameworks = setOf(
    "expo", "react-native", "reactnative", "rn", "react", "flutter",
    "nextjs", "next", "vite", "web", "remix", "astro", "svelte",
)
private val mobileFrameworks = setOf(
    "expo", "react-native", "reactnative", "rn", "flutter", "swift", "kotlin", "android",
)
private val renderedSurfaces = setOf(
    "mobile", "ios", "android", "web", "frontend", "browser", "tv", "tvos", "tvos-simulator",
)

/** Same product contract as tvOS VibingPlan: Vibing lists visible apps, not
 * backend-only repositories or the daemon's accidentally-discovered home. */
fun isRenderableVibingProject(project: ProjectRow): Boolean {
    val frameworks = (project.frameworks + listOfNotNull(project.framework)).map(String::lowercase).toSet()
    if (frameworks.any { it in webFrameworks || it in mobileFrameworks }) return true
    return (project.surfaces + project.testSurfaces).any { it.lowercase() in renderedSurfaces }
}

fun filteredVibingProjects(projects: List<ProjectRow>, query: String): List<ProjectRow> {
    val needle = query.trim().lowercase()
    return projects.filter { project ->
        isRenderableVibingProject(project) && (needle.isEmpty() || listOfNotNull(
            project.name,
            project.framework,
            project.branch,
        ).plus(project.frameworks).plus(project.surfaces).plus(project.testSurfaces)
            .joinToString(" ").lowercase().contains(needle))
    }
}

fun vibingDisplayFramework(project: ProjectRow): String? =
    (listOfNotNull(project.framework) + project.frameworks).firstOrNull {
        it.lowercase() in webFrameworks || it.lowercase() in mobileFrameworks
    } ?: project.framework

fun vibingCapabilityLabels(project: ProjectRow): List<String> {
    val frameworks = (listOfNotNull(project.framework) + project.frameworks).map(String::lowercase)
    val surfaces = (project.surfaces + project.testSurfaces).map(String::lowercase).toSet()
    return buildList {
        if (frameworks.any { it in mobileFrameworks } || surfaces.any { it in setOf("mobile", "ios", "android") }) add("Mobile")
        if (frameworks.any { it in webFrameworks } || surfaces.any { it in setOf("web", "browser", "frontend") }) add("Web")
        if (frameworks.any { it in setOf("nextjs", "next", "vite", "react", "web", "remix", "astro", "svelte") }) add("Frontend")
        if (surfaces.any { it in setOf("tv", "tvos", "tvos-simulator") }) add("TV")
    }.distinct()
}

fun devServerOwnsProject(activeWorkDir: String?, selectedWorkDir: String): Boolean =
    activeWorkDir?.trim()?.trimEnd('/')?.takeIf(String::isNotEmpty) == selectedWorkDir.trim().trimEnd('/')

/** URL consumed by Chromium on the render box, not by the television. Prefer
 * the agent's same-origin proxies and turn relative paths into agent-loopback
 * URLs, matching the tvOS capture lane. */
fun vibingCaptureTarget(dev: JSONObject, web: JSONObject = JSONObject()): String {
    val webUrl = web.optString("webUrl")
    if (webUrl.startsWith("/")) return "http://127.0.0.1:$AGENT_PORT$webUrl"
    if (webUrl.startsWith("http://") || webUrl.startsWith("https://")) return webUrl

    val bundleUrl = dev.optString("bundleUrl")
    if (bundleUrl.startsWith("/")) return "http://127.0.0.1:$AGENT_PORT$bundleUrl"

    val url = dev.optString("url")
    if (url.startsWith("http://") || url.startsWith("https://")) return url

    val directUrl = dev.optString("directUrl")
    if (directUrl.startsWith("http://") || directUrl.startsWith("https://")) {
        runCatching { URI(directUrl).port }.getOrNull()?.takeIf { it > 0 }?.let {
            return "http://127.0.0.1:$it"
        }
    }

    val port = dev.optInt("port", web.optInt("port", 0))
    return if (port > 0) "http://127.0.0.1:$port" else ""
}

fun org.json.JSONArray?.toStringList(): List<String> {
    if (this == null) return emptyList()
    return buildList {
        for (index in 0 until length()) optString(index).takeIf(String::isNotEmpty)?.let(::add)
    }
}
