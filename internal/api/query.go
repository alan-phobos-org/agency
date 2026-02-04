package api

import (
	"fmt"
	"strconv"
)

// ParseIntParam parses an integer query parameter with bounds validation.
// Returns defaultVal if value is empty.
// Returns error if value is invalid or out of bounds [min, max].
func ParseIntParam(value string, min, max, defaultVal int) (int, error) {
	if value == "" {
		return defaultVal, nil
	}
	v, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("must be a valid integer")
	}
	if v < min || v > max {
		return 0, fmt.Errorf("must be between %d and %d", min, max)
	}
	return v, nil
}
