# Built-in probe library. Vendors statically link this into their
# c-agent main and call the registrars to bake probes into the
# binary. Compiles on any POSIX system; on platforms without `iw`
# the probe returns an empty (but valid) CBOR document.

add_library(yvr_cagent_probes STATIC
    src/wifi_client_count.c
)
add_library(yaver::cagent_probes ALIAS yvr_cagent_probes)

target_include_directories(yvr_cagent_probes
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>
)
target_link_libraries(yvr_cagent_probes PUBLIC
    yvr_cagent_core
    yvr_cagent_host
)
set_target_properties(yvr_cagent_probes PROPERTIES
    POSITION_INDEPENDENT_CODE ON
    C_VISIBILITY_PRESET hidden
    EXPORT_NAME cagent_probes
)

if (YVR_CAGENT_INSTALL)
    install(
        TARGETS yvr_cagent_probes
        EXPORT  yaver_cagentTargets
        LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}"
        INCLUDES DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}"
    )
endif()
