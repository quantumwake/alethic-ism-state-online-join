package utils

import (
	"fmt"
	"time"
)

func FormatNanoSeconds(ns int64) string {
	// Calculate each time unit
	hours := ns / int64(time.Hour)
	ns %= int64(time.Hour)

	minutes := ns / int64(time.Minute)
	ns %= int64(time.Minute)

	seconds := ns / int64(time.Second)
	ns %= int64(time.Second)

	milliseconds := ns / int64(time.Millisecond)
	ns %= int64(time.Millisecond)

	// The remaining ns are the nanoseconds
	nanoseconds := ns

	return fmt.Sprintf("%d hours, %d minutes, %d seconds, %d milliseconds, %d nanoseconds",
		hours, minutes, seconds, milliseconds, nanoseconds)
}
