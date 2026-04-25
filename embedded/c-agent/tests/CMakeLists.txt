add_executable(test_frame test_frame.c)
target_link_libraries(test_frame PRIVATE yvr_cagent_core)
add_test(NAME frame COMMAND test_frame)

add_executable(test_cbor test_cbor.c)
target_link_libraries(test_cbor PRIVATE yvr_cagent_core)
add_test(NAME cbor COMMAND test_cbor)

add_executable(test_body test_body.c)
target_link_libraries(test_body PRIVATE yvr_cagent_core)
add_test(NAME body COMMAND test_body)

add_executable(test_host test_host.c)
target_link_libraries(test_host PRIVATE yvr_cagent_host)
add_test(NAME host COMMAND test_host)

if (TARGET yvr_cagent_tcp)
    find_package(Threads REQUIRED)
    add_executable(test_tcp test_tcp.c)
    target_link_libraries(test_tcp PRIVATE yvr_cagent_tcp Threads::Threads)
    add_test(NAME tcp COMMAND test_tcp)
endif()

add_executable(test_probes test_probes.c)
target_link_libraries(test_probes PRIVATE yvr_cagent_probes)
add_test(NAME probes COMMAND test_probes)
