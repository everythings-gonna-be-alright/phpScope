package converter

// SampleTypeConfig defines configuration for different sample types in the profiler
var SampleTypeConfig = map[string]map[string]interface{}{
	"cpu": {
		"units":        "nanoseconds",
		"display-name": "cpu-time",
		"aggregation":  "sum",
		"cumulative":   false,
		"sampled":      true,
	},
	"samples": {
		"units":        "count",
		"display-name": "sample-count",
		"aggregation":  "sum",
		"cumulative":   false,
		"sampled":      true,
	},
	// "memory": {
	// 	"units":       "bytes",
	// 	"aggregation": "avg",
	// 	"sampled":     false,
	// },
}
