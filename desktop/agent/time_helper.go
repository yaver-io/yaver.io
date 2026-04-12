package main

import "time"

// toTime converts a unix epoch (seconds) to time.Time.
func toTime(sec int64) time.Time { return time.Unix(sec, 0).UTC() }
