package rank

import (
	"testing"

	"github.com/omervaroglu/asokit/internal/itunes"
)

func TestFind(t *testing.T) {
	results := []itunes.App{
		{ID: 100, Name: "First"},
		{ID: 200, Name: "Second"},
		{ID: 300, Name: "Third"},
	}

	tests := []struct {
		name     string
		appID    int64
		results  []itunes.App
		wantPos  int
		wantFind bool
	}{
		{"first position", 100, results, 1, true},
		{"middle position", 200, results, 2, true},
		{"last position", 300, results, 3, true},
		{"not present", 999, results, 0, false},
		{"empty results", 100, nil, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Find(tt.appID, tt.results)
			if got.Position != tt.wantPos {
				t.Errorf("Position = %d, want %d", got.Position, tt.wantPos)
			}
			if got.Found != tt.wantFind {
				t.Errorf("Found = %v, want %v", got.Found, tt.wantFind)
			}
			if got.ResultCount != len(tt.results) {
				t.Errorf("ResultCount = %d, want %d", got.ResultCount, len(tt.results))
			}
		})
	}
}
