//go:build integration

package bqsink_test

import (
	"context"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/bigquery"
	"google.golang.org/api/iterator"

	"github.com/pytsekas/experiment-go/internal/consumption"
	"github.com/pytsekas/experiment-go/internal/consumption/bqsink"
)

// TestWriteRoundTrip appends a row to a real development table and reads it
// back. Run it with: go test -tags integration ./internal/consumption/bqsink/
// and BQ_PROJECT / BQ_DATASET set to a dataset you may write to.
func TestWriteRoundTrip(t *testing.T) {
	project, dataset := os.Getenv("BQ_PROJECT"), os.Getenv("BQ_DATASET")
	if project == "" || dataset == "" {
		t.Skip("BQ_PROJECT and BQ_DATASET must be set")
	}

	ctx := context.Background()
	sink, err := bqsink.New(ctx, bqsink.Config{Project: project, Dataset: dataset, Table: "readings"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if err := sink.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	messageID := "it-" + time.Now().UTC().Format("20060102150405.000")
	row := consumption.Reading{
		MeteringPointID: "IT-METER",
		Start:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Resolution:      time.Hour,
		Value:           42,
		Unit:            "kWh",
		Quality:         consumption.QualityMeasured,
		Direction:       consumption.DirectionConsumption,
		Measure:         consumption.MeasureGross,
		SourceMessageID: messageID,
		IngestedAt:      time.Now().UTC(),
	}

	if err := sink.Write(ctx, []consumption.Reading{row}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	client, err := bigquery.NewClient(ctx, project)
	if err != nil {
		t.Fatalf("bigquery client: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Errorf("bigquery client close: %v", err)
		}
	}()

	q := client.Query("SELECT COUNT(*) AS n FROM `" + dataset + ".readings` WHERE source_message_id = @id")
	q.Parameters = []bigquery.QueryParameter{{Name: "id", Value: messageID}}

	it, err := q.Read(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	var result struct{ N int64 }
	if err := it.Next(&result); err != nil && err != iterator.Done {
		t.Fatalf("next: %v", err)
	}
	if result.N != 1 {
		t.Fatalf("got %d rows, want 1", result.N)
	}
}
