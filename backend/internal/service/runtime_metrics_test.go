package service

import "testing"

func TestRuntimeMetricsTracksWaitingByScope(t *testing.T) {
	metrics := NewRuntimeMetrics()
	request := metrics.Begin("openai", 7, 11)
	request.SetWaiting(true)
	request.SetWaiting(true)

	all := metrics.Snapshot("", 0)
	if all.InFlight != 1 || all.Waiting != 1 {
		t.Fatalf("global snapshot = %+v", all)
	}
	group := metrics.Snapshot("", 7)
	if group.InFlight != 1 || group.Waiting != 1 {
		t.Fatalf("group snapshot = %+v", group)
	}
	platform := metrics.Snapshot("openai", 0)
	if platform.InFlight != 1 || platform.Waiting != 1 {
		t.Fatalf("platform snapshot = %+v", platform)
	}

	request.SetWaiting(false)
	if got := metrics.Snapshot("", 0).Waiting; got != 0 {
		t.Fatalf("waiting after release = %d", got)
	}
	request.Finish()
	if got := metrics.Snapshot("", 0).InFlight; got != 0 {
		t.Fatalf("in-flight after finish = %d", got)
	}
}

func TestRuntimeMetricsFinishReleasesWaiting(t *testing.T) {
	metrics := NewRuntimeMetrics()
	request := metrics.Begin("anthropic", 9, 12)
	request.SetWaiting(true)
	request.Finish()

	snapshot := metrics.Snapshot("", 0)
	if snapshot.InFlight != 0 || snapshot.Waiting != 0 {
		t.Fatalf("finish leaked runtime counts: %+v", snapshot)
	}
}

func TestRuntimeMetricsMovesRequestBetweenGroups(t *testing.T) {
	metrics := NewRuntimeMetrics()
	request := metrics.Begin("openai", 7, 11)
	request.SetWaiting(true)
	request.SetGroup(8)

	if snapshot := metrics.Snapshot("", 7); snapshot.InFlight != 0 || snapshot.Waiting != 0 {
		t.Fatalf("old group retained counts: %+v", snapshot)
	}
	if snapshot := metrics.Snapshot("", 8); snapshot.InFlight != 1 || snapshot.Waiting != 1 {
		t.Fatalf("new group did not receive counts: %+v", snapshot)
	}
	request.Finish()
	if snapshot := metrics.Snapshot("", 8); snapshot.InFlight != 0 || snapshot.Waiting != 0 {
		t.Fatalf("finish leaked moved counts: %+v", snapshot)
	}
}
