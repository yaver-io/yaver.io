package main

import "github.com/docker/docker/api/types/container"

func containerLogsOpts() container.LogsOptions {
	return container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: true, Tail: "200", Timestamps: true,
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
