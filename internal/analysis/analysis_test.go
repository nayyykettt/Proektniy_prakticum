package analysis

import (
	"testing"

	"teampulse/internal/metrics"
)

func f(v float64) *float64 { return &v }

func TestTrends(t *testing.T) {
	cur := metrics.Summary{Commits: 110, MRLeadTimeMedianH: f(10), TestRatio: f(20), BusFactor: 2}
	prev := metrics.Summary{Commits: 100, MRLeadTimeMedianH: f(20), TestRatio: f(40), BusFactor: 2}
	got := map[string]Trend{}
	for _, tr := range Trends(cur, prev) {
		got[tr.Key] = tr
	}
	cases := map[string]Verdict{
		"commits":               Changed,  // нейтральная метрика
		"mr_lead_time_median_h": Improved, // меньше — лучше
		"test_ratio":            Worsened,
		"bus_factor":            Stable,
		"rework_rate":           NoData,
	}
	for k, want := range cases {
		if got[k].Verdict != want {
			t.Errorf("%s: got %s, want %s", k, got[k].Verdict, want)
		}
	}
	if d := got["mr_lead_time_median_h"].DeltaPct; d == nil || *d != -50 {
		t.Errorf("delta: %v", d)
	}
}

func TestRecommend(t *testing.T) {
	s := metrics.Snapshot{
		Summary: metrics.Summary{Commits: 50, BusFactor: 1, FilesTracked: 40, MRMerged: 10,
			MRLeadTimeMedianH: f(100), ReviewCoverage: f(40), TestRatio: f(5), OffHoursShare: f(30)},
		Ownership: []metrics.OwnerShare{{Name: "Иван", Share: 80}},
		Authors:   []metrics.AuthorStats{{Name: "Иван", Commits: 40, OffHoursShare: f(35)}},
	}
	recs := Recommend(s, nil)
	if len(recs) == 0 || recs[0].Severity != Critical || recs[0].Metric != "bus_factor" {
		t.Fatalf("первой должна быть критичная рекомендация по bus factor: %+v", recs)
	}
	seen := map[string]bool{}
	for _, r := range recs {
		seen[r.Metric] = true
	}
	for _, m := range []string{"mr_lead_time_median_h", "review_coverage", "test_ratio", "off_hours_share", "mr_merged"} {
		if !seen[m] {
			t.Errorf("нет рекомендации по %s", m)
		}
	}

	empty := Recommend(metrics.Snapshot{}, nil)
	if len(empty) != 1 || empty[0].Metric != "commits" {
		t.Fatalf("пустой период: %+v", empty)
	}
}
