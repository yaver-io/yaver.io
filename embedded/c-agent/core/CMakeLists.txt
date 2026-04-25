add_library(yvr_cagent_core STATIC
    src/body.c
    src/cbor.c
    src/frame.c
    src/status.c
)
add_library(yaver::cagent_core ALIAS yvr_cagent_core)

target_include_directories(yvr_cagent_core
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>
)

set_target_properties(yvr_cagent_core PROPERTIES
    POSITION_INDEPENDENT_CODE ON
    C_VISIBILITY_PRESET hidden
    EXPORT_NAME cagent_core
)

if (YVR_CAGENT_INSTALL)
    install(
        TARGETS yvr_cagent_core
        EXPORT  yaver_cagentTargets
        LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}"
        INCLUDES DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}"
    )
endif()
