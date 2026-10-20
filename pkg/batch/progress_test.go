package batch

import "testing"

func TestTotalsCountEachURLOnceAcrossStages(t *testing.T) {
	counts := Counts{
		httpQueue:    {Pending: 40, Running: 5, Done: 50, Dead: 3, Failed: 2},
		browserQueue: {Pending: 4, Running: 2, Retrying: 1, Done: 10, Dead: 2, Failed: 1},
	}
	got := counts.Totals()
	want := Totals{URLs: 100, Done: 40, Dead: 5, Failed: 3}
	if got != want {
		t.Fatalf("Totals() = %+v, want %+v", got, want)
	}
	if got.Finished() != 48 {
		t.Fatalf("Finished() = %d, want 48", got.Finished())
	}
}
