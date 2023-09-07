package cmpfeeds

import (
	"fmt"
	"sort"
)

var percentiles = []int{10, 25, 50, 75, 90}

func calculatePercentiles(data []int64) string {
	if len(data) == 0 {
		return ""
	}
	sort.Slice(data, func(i, j int) bool {
		return data[i] < data[j]
	})

	result := ""
	for _, percentile := range percentiles {
		result += fmt.Sprintf("P%d: %dμs ", percentile, calculatePercentile(data, float64(percentile)))
	}

	return result
}

func calculatePercentile(data []int64, percentile float64) int64 {
	index := int((percentile / 100) * float64(len(data)-1))
	return data[index]
}
