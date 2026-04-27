package io.yaver.fixture.native_android

/**
 * Yaver fixture authentication helper.
 * Hardcoded creds (admin / admin) — DO NOT use as production auth pattern.
 */
object Auth {
    const val VALID_USERNAME = "admin"
    const val VALID_PASSWORD = "admin"

    fun authenticate(username: String, password: String): Boolean {
        return username == VALID_USERNAME && password == VALID_PASSWORD
    }
}
