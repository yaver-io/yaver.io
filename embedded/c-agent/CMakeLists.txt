cmake_minimum_required(VERSION 3.16)
project(yaver_cagent
    VERSION 0.0.1
    LANGUAGES C
    DESCRIPTION "Yaver c-agent — device-side runtime for AI-driven IoT troubleshooting"
)

# c-agent is an additional, opt-in Yaver surface — see
# docs/c-agent-architecture.md "Status". Nothing here is wired into
# Yaver's main build or CI. Run ./build.sh to build standalone.

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_C_EXTENSIONS OFF)

if (NOT CMAKE_BUILD_TYPE)
    set(CMAKE_BUILD_TYPE Debug)
endif()

# Strict warnings everywhere; -Werror is opt-in via YVR_CAGENT_WERROR
# so a warning from a new compiler version doesn't block local builds.
add_compile_options(
    -Wall
    -Wextra
    -Wpedantic
    -Wshadow
    -Wstrict-prototypes
    -Wmissing-prototypes
    -Wpointer-arith
    -Wcast-align
    -Wwrite-strings
    -Wundef
    -Wno-unused-parameter
    -fno-strict-aliasing
)

option(YVR_CAGENT_TESTS    "Build unit tests"                    ON)
option(YVR_CAGENT_ASAN     "Build with AddressSanitizer + UBSan" OFF)
option(YVR_CAGENT_WERROR   "Treat warnings as errors"            OFF)
option(YVR_CAGENT_INSTALL  "Generate install + export rules"     ON)

if (YVR_CAGENT_WERROR)
    add_compile_options(-Werror)
endif()

if (YVR_CAGENT_ASAN)
    add_compile_options(-fsanitize=address,undefined -fno-omit-frame-pointer -O1 -g)
    add_link_options(-fsanitize=address,undefined)
endif()

include(GNUInstallDirs)
include(CMakePackageConfigHelpers)

# The generated .pc and yaver-cagent-config script are self-relocating
# — they derive `prefix` from their own location at runtime. We pass
# only the relative suffixes (e.g. "lib", "include") into the
# templates so the install can be moved to any path post-build.
#
# Absolute CMAKE_INSTALL_LIBDIR / INCLUDEDIR break relocation; reject
# loudly so vendors don't ship a non-portable install.
if (IS_ABSOLUTE "${CMAKE_INSTALL_LIBDIR}")
    message(FATAL_ERROR
        "CMAKE_INSTALL_LIBDIR must be relative for relocatable installs; "
        "got '${CMAKE_INSTALL_LIBDIR}'")
endif()
if (IS_ABSOLUTE "${CMAKE_INSTALL_INCLUDEDIR}")
    message(FATAL_ERROR
        "CMAKE_INSTALL_INCLUDEDIR must be relative for relocatable installs; "
        "got '${CMAKE_INSTALL_INCLUDEDIR}'")
endif()
set(LIBDIR_SUFFIX     "${CMAKE_INSTALL_LIBDIR}")
set(INCLUDEDIR_SUFFIX "${CMAKE_INSTALL_INCLUDEDIR}")

add_subdirectory(core)
add_subdirectory(transports)   # before host so host can reference TCP target
add_subdirectory(host)
add_subdirectory(probes)

if (YVR_CAGENT_TESTS)
    enable_testing()
    add_subdirectory(tests)
endif()

if (YVR_CAGENT_INSTALL)
    # ── pkg-config ────────────────────────────────────────────────
    configure_file(
        cmake/yaver-cagent.pc.in
        "${CMAKE_BINARY_DIR}/yaver-cagent.pc"
        @ONLY
    )
    install(
        FILES "${CMAKE_BINARY_DIR}/yaver-cagent.pc"
        DESTINATION "${CMAKE_INSTALL_LIBDIR}/pkgconfig"
    )

    # ── ncurses-style shell config ───────────────────────────────
    configure_file(
        cmake/yaver-cagent-config.in
        "${CMAKE_BINARY_DIR}/yaver-cagent-config"
        @ONLY
    )
    install(
        PROGRAMS "${CMAKE_BINARY_DIR}/yaver-cagent-config"
        DESTINATION "${CMAKE_INSTALL_BINDIR}"
    )

    # ── CMake config (find_package) ──────────────────────────────
    set(YVR_CAGENT_CONFIG_DIR "${CMAKE_INSTALL_LIBDIR}/cmake/yaver_cagent")

    configure_package_config_file(
        cmake/yaver_cagentConfig.cmake.in
        "${CMAKE_BINARY_DIR}/yaver_cagentConfig.cmake"
        INSTALL_DESTINATION "${YVR_CAGENT_CONFIG_DIR}"
    )
    write_basic_package_version_file(
        "${CMAKE_BINARY_DIR}/yaver_cagentConfigVersion.cmake"
        VERSION "${PROJECT_VERSION}"
        COMPATIBILITY SameMinorVersion
    )
    install(
        FILES
            "${CMAKE_BINARY_DIR}/yaver_cagentConfig.cmake"
            "${CMAKE_BINARY_DIR}/yaver_cagentConfigVersion.cmake"
        DESTINATION "${YVR_CAGENT_CONFIG_DIR}"
    )
    install(
        EXPORT yaver_cagentTargets
        FILE yaver_cagentTargets.cmake
        NAMESPACE yaver::
        DESTINATION "${YVR_CAGENT_CONFIG_DIR}"
    )

    # ── Public headers ───────────────────────────────────────────
    install(
        DIRECTORY core/include/yvr
        DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}"
        FILES_MATCHING PATTERN "*.h"
    )
    install(
        DIRECTORY host/include/yvr
        DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}"
        FILES_MATCHING PATTERN "*.h"
    )
    install(
        DIRECTORY transports/include/yvr
        DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}"
        FILES_MATCHING PATTERN "*.h"
    )
    install(
        DIRECTORY probes/include/yvr
        DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}"
        FILES_MATCHING PATTERN "*.h"
    )
endif()
