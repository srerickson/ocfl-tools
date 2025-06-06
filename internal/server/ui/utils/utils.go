package utils

import "fmt"

func FileSize(byteSize int64) string {
	var units = []string{"Bytes", "KB", "MB", "GB", "TB"}
	scaled := float64(byteSize)
	unit := ""
	for _, u := range units {
		unit = u
		if scaled < 1000 {
			break
		}
		scaled = scaled / 1000
	}
	if unit == "Bytes" {
		return fmt.Sprintf("%d %s", int64(scaled), unit)
	}
	return fmt.Sprintf("%0.2f %s", scaled, unit)
}
