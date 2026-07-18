package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClientConcurrencyLimiterWaitsAndReleases(t *testing.T) {
	limiter := NewClientConcurrencyLimiter()
	first, _, err := limiter.Acquire(context.Background(), 1, 1, 10, 1, time.Second, 4)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		lease *ClientConcurrencyLease
		err   error
	}
	done := make(chan result, 1)
	go func() {
		lease, _, err := limiter.Acquire(context.Background(), 1, 1, 10, 1, time.Second, 4)
		done <- result{lease: lease, err: err}
	}()
	time.Sleep(20 * time.Millisecond)
	if got := limiter.Snapshot().Waiting; got != 1 {
		t.Fatalf("waiting = %d", got)
	}
	first.Release()
	select {
	case acquired := <-done:
		if acquired.err != nil || acquired.lease == nil {
			t.Fatalf("second acquire = %#v, %v", acquired.lease, acquired.err)
		}
		acquired.lease.Release()
	case <-time.After(time.Second):
		t.Fatal("waiting acquire was not released")
	}
	if snapshot := limiter.Snapshot(); len(snapshot.Users) != 0 || len(snapshot.APIKeys) != 0 || snapshot.Waiting != 0 {
		t.Fatalf("leaked concurrency state: %#v", snapshot)
	}
}

func TestClientConcurrencyLimiterQueueFullAndTimeout(t *testing.T) {
	limiter := NewClientConcurrencyLimiter()
	first, _, err := limiter.Acquire(context.Background(), 1, 1, 1, 1, time.Second, 1)
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan error, 1)
	go func() {
		_, _, err := limiter.Acquire(context.Background(), 1, 1, 2, 0, 40*time.Millisecond, 1)
		waiting <- err
	}()
	time.Sleep(10 * time.Millisecond)
	if _, _, err := limiter.Acquire(context.Background(), 1, 1, 3, 0, time.Second, 1); !errors.Is(err, ErrConcurrencyQueueFull) {
		t.Fatalf("queue-full error = %v", err)
	}
	if err := <-waiting; !errors.Is(err, ErrConcurrencyWaitTimeout) {
		t.Fatalf("timeout error = %v", err)
	}
	first.Release()
}

func TestClientConcurrencyLimiterCancellationDoesNotLeakWaiter(t *testing.T) {
	limiter := NewClientConcurrencyLimiter()
	first, _, err := limiter.Acquire(context.Background(), 1, 1, 1, 0, time.Second, 4)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := limiter.Acquire(ctx, 1, 1, 2, 0, time.Second, 4); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if got := limiter.Snapshot().Waiting; got != 0 {
		t.Fatalf("waiting leaked after cancellation: %d", got)
	}
	first.Release()
}

func TestClientConcurrencyLimiterBroadcastsReleasedCapacity(t *testing.T) {
	limiter := NewClientConcurrencyLimiter()
	first, _, err := limiter.Acquire(context.Background(), 1, 2, 1, 2, time.Second, 8)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := limiter.Acquire(context.Background(), 1, 2, 1, 2, time.Second, 8)
	if err != nil {
		t.Fatal(err)
	}

	results := make(chan *ClientConcurrencyLease, 2)
	for range 2 {
		go func() {
			lease, _, acquireErr := limiter.Acquire(context.Background(), 1, 2, 1, 2, time.Second, 8)
			if acquireErr != nil {
				results <- nil
				return
			}
			results <- lease
		}()
	}
	deadline := time.Now().Add(time.Second)
	for limiter.Snapshot().Waiting != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	first.Release()
	second.Release()
	for range 2 {
		select {
		case lease := <-results:
			if lease == nil {
				t.Fatal("waiter failed after capacity was released")
			}
			lease.Release()
		case <-time.After(time.Second):
			t.Fatal("released capacity did not wake every waiter")
		}
	}
}
