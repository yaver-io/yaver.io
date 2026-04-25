# Optional transport adapters. Each transport is its own static
# library so a vendor build that doesn't need TCP (e.g. BLE-only
# device) doesn't link it.
#
# The TCP adapter is POSIX-only. Skip it on platforms whose libc
# doesn't have the standard <sys/socket.h> family.

if (UNIX OR APPLE)
    add_library(yvr_cagent_tcp STATIC
        src/tcp.c
    )
    add_library(yaver::cagent_tcp ALIAS yvr_cagent_tcp)

    target_include_directories(yvr_cagent_tcp
        PUBLIC
            $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
            $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>
    )
    target_link_libraries(yvr_cagent_tcp PUBLIC yvr_cagent_core)
    set_target_properties(yvr_cagent_tcp PROPERTIES
        POSITION_INDEPENDENT_CODE ON
        C_VISIBILITY_PRESET hidden
        EXPORT_NAME cagent_tcp
    )

    if (YVR_CAGENT_INSTALL)
        install(
            TARGETS yvr_cagent_tcp
            EXPORT  yaver_cagentTargets
            LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
            ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
            RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}"
            INCLUDES DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}"
        )
    endif()
endif()
