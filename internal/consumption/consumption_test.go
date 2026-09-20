package consumption_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/pytsekas/experiment-go/internal/consumption"
)

func validReading() consumption.Reading {
	return consumption.Reading{
		MeteringPointID: "EE-123",
		Start:           time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Resolution:      time.Hour,
		Value:           12.5,
		Unit:            "kWh",
		Quality:         consumption.QualityMeasured,
		Direction:       consumption.DirectionConsumption,
	}
}

func TestReadingValidate(t *testing.T) {
	tests := map[string]struct {
		mutate  func(*consumption.Reading)
		wantErr bool
	}{
		"valid":               {mutate: func(*consumption.Reading) {}},
		"blank meter":         {mutate: func(r *consumption.Reading) { r.MeteringPointID = "  " }, wantErr: true},
		"zero start":          {mutate: func(r *consumption.Reading) { r.Start = time.Time{} }, wantErr: true},
		"zero resolution":     {mutate: func(r *consumption.Reading) { r.Resolution = 0 }, wantErr: true},
		"negative resolution": {mutate: func(r *consumption.Reading) { r.Resolution = -time.Hour }, wantErr: true},
		"NaN value":           {mutate: func(r *consumption.Reading) { r.Value = math.NaN() }, wantErr: true},
		"Inf value":           {mutate: func(r *consumption.Reading) { r.Value = math.Inf(1) }, wantErr: true},
		"blank unit":          {mutate: func(r *consumption.Reading) { r.Unit = "" }, wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r := validReading()
			tc.mutate(&r)

			err := r.Validate()
			if tc.wantErr && !errors.Is(err, consumption.ErrInvalidReading) {
				t.Fatalf("got %v, want ErrInvalidReading", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("got %v, want nil", err)
			}
		})
	}
}
