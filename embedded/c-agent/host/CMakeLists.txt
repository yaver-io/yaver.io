add_library(yvr_cagent_host STATIC
    src/host.c
    src/session.c
)
target_link_libraries(yvr_cagent_host PUBLIC yvr_cagent_core)
if (TARGET yvr_cagent_tcp)
    target_link_libraries(yvr_cagent_host PUBLIC yvr_cagent_tcp)
endif()
add_library(yaver::cagent_host ALIAS yvr_cagent_host)

target_include_directories(yvr_cagent_host
    PUBLIC
        $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
        $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>
)

set_target_properties(yvr_cagent_host PROPERTIES
    POSITION_INDEPENDENT_CODE ON
    C_VISIBILITY_PRESET hidden
    EXPORT_NAME cagent_host
)

if (YVR_CAGENT_INSTALL)
    install(
        TARGETS yvr_cagent_host
        EXPORT  yaver_cagentTargets
        LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}"
        INCLUDES DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}"
    )
endif()
