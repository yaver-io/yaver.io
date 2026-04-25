cmake_minimum_required(VERSION 3.16)
project(example_cmake C)

# Consumes the c-agent runtime via CMake's `find_package`.
# Run with:
#
#   cmake -S . -B build \
#     -DCMAKE_PREFIX_PATH=/path/to/c-agent/install
#   cmake --build build
#   ./build/example
#
# CMAKE_PREFIX_PATH points at the install root produced by:
#
#   cmake --install ../../build --prefix /path/to/c-agent/install

find_package(yaver_cagent REQUIRED)

add_executable(example main.c)
target_link_libraries(example PRIVATE yaver::cagent_core yaver::cagent_host)
