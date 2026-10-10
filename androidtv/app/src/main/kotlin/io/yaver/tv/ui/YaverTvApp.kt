package io.yaver.tv.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.SideEffect
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import io.yaver.tv.TvStore

/** Route constants — one place so the dashboard tiles and the host agree. */
object Routes {
    const val SIGN_IN = "signin"
    const val DASHBOARD = "dashboard"
    const val MACHINES = "machines"
    const val ADD_BOX = "addbox"
    const val UPDATE_AGENT = "update"
    const val SETTINGS = "settings"
    const val TASKS = "tasks"
    const val COMPOSER = "composer"
    const val SESSION = "session"
    const val VIBING = "vibing"
    const val STUDIO = "studio"
    const val SSH = "ssh"
    const val DROID = "droid"
    const val TASK_DETAIL = "task/{taskId}"
    const val PREVIEW = "preview/{projectName}"

    fun taskDetail(taskId: String) = "task/$taskId"
    fun preview(projectName: String) = "preview/${android.net.Uri.encode(projectName)}"
}

/**
 * YaverTvApp — the single navigation host + binary auth gate (tvOS
 * RootView). Signed-in → Dashboard; otherwise SignIn. Every pushed screen gets
 * the shared store, so the token/selected-box/relay state is one instance.
 */
@Composable
fun YaverTvApp(store: TvStore) {
    val nav = rememberNavController()
    val token by store.token.collectAsState()
    val authenticated = token.isNotEmpty()
    val appearanceTheme by store.appearanceTheme.collectAsState()
    SideEffect { TvColors.applyTheme(appearanceTheme) }

    LaunchedEffect(authenticated) {
        if (authenticated) {
            nav.navigate(Routes.DASHBOARD) {
                popUpTo(Routes.SIGN_IN) { inclusive = true }
                launchSingleTop = true
            }
        } else {
            nav.navigate(Routes.SIGN_IN) {
                popUpTo(Routes.DASHBOARD) { inclusive = true }
                launchSingleTop = true
            }
        }
    }

    MaterialTheme(
        colorScheme = if (appearanceTheme == "light") lightColorScheme(
            primary = TvColors.Accent,
            background = TvColors.Bg,
            surface = TvColors.Card,
            onBackground = TvColors.TextPrimary,
            onSurface = TvColors.TextPrimary,
        ) else darkColorScheme(
            primary = TvColors.Accent,
            background = TvColors.Bg,
            surface = TvColors.Card,
            onBackground = TvColors.TextPrimary,
            onSurface = TvColors.TextPrimary,
        ),
    ) {
      NavHost(
          navController = nav,
          startDestination = if (authenticated) Routes.DASHBOARD else Routes.SIGN_IN,
      ) {
        composable(Routes.SIGN_IN) { SignInScreen(store = store) }
        composable(Routes.DASHBOARD) { DashboardScreen(store = store, nav = nav) }
        composable(Routes.MACHINES) { MachinePickerScreen(store = store, nav = nav) }
        composable(Routes.ADD_BOX) { AddBoxScreen(store = store, nav = nav) }
        composable(Routes.UPDATE_AGENT) { UpdateAgentScreen(store = store, nav = nav) }
        composable(Routes.SETTINGS) { SettingsScreen(store = store, nav = nav) }
        composable(Routes.TASKS) { TasksScreen(store = store, nav = nav) }
        composable(Routes.COMPOSER) { TaskComposerScreen(store = store, nav = nav) }
        composable(Routes.SESSION) { SessionScreen(store = store, nav = nav) }
        composable(Routes.VIBING) { VibingScreen(store = store, nav = nav) }
        composable(Routes.STUDIO) { StudioScreen(store = store, nav = nav) }
        composable(Routes.SSH) { SshScreen(store = store, nav = nav) }
        composable(Routes.DROID) { DroidStreamScreen(store = store, nav = nav) }
        composable(
            Routes.TASK_DETAIL,
            arguments = listOf(navArgument("taskId") { defaultValue = "" }),
        ) { entry ->
            TaskDetailScreen(store = store, nav = nav, taskId = entry.arguments?.getString("taskId").orEmpty())
        }
        composable(
            Routes.PREVIEW,
            arguments = listOf(navArgument("projectName") { defaultValue = "" }),
        ) { entry ->
            PreviewStreamScreen(
                store = store,
                nav = nav,
                projectName = entry.arguments?.getString("projectName").orEmpty(),
            )
        }
      }
    }
}
