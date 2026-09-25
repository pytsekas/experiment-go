package bqsink

import (
	"time"

	"cloud.google.com/go/bigquery"

	"github.com/pytsekas/experiment-go/internal/consumption"
)

// TableSchema is the readings table, in the field order Terraform creates it
// with. The two definitions must agree; the integration test is what proves
// they still do.
func TableSchema() bigquery.Schema {
	return bigquery.Schema{
		{Name: "metering_point_id", Type: bigquery.StringFieldType, Required: true},
		{Name: "interval_start", Type: bigquery.TimestampFieldType, Required: true},
		{Name: "resolution_sec", Type: bigquery.IntegerFieldType, Required: true},
		{Name: "value", Type: bigquery.FloatFieldType, Required: true},
		{Name: "unit", Type: bigquery.StringFieldType, Required: true},
		{Name: "quality", Type: bigquery.StringFieldType, Required: true},
		{Name: "direction", Type: bigquery.StringFieldType, Required: true},
		{Name: "measure", Type: bigquery.StringFieldType, Required: true},
		{Name: "source_message_id", Type: bigquery.StringFieldType, Required: true},
		{Name: "ingested_at", Type: bigquery.TimestampFieldType, Required: true},
	}
}

// readingToValues maps a reading onto the schema's field names. BigQuery
// timestamps are microseconds since the epoch on the wire.
func readingToValues(r consumption.Reading) map[string]any {
	return map[string]any{
		"metering_point_id": r.MeteringPointID,
		"interval_start":    r.Start.UTC().UnixMicro(),
		"resolution_sec":    int64(r.Resolution / time.Second),
		"value":             r.Value,
		"unit":              r.Unit,
		"quality":           r.Quality,
		"direction":         r.Direction,
		"measure":           r.Measure,
		"source_message_id": r.SourceMessageID,
		"ingested_at":       r.IngestedAt.UTC().UnixMicro(),
	}
}
