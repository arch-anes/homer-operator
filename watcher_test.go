package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

func TestRunWatcherLoopHandlesEventsAndStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fakeWatcher := watch.NewFake()
	eventHandled := make(chan struct{}, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		runWatcherLoop(ctx, "test", func(context.Context) (watch.Interface, error) {
			return fakeWatcher, nil
		}, func(context.Context, watch.Event) {
			eventHandled <- struct{}{}
		}, time.Millisecond, logrus.New())
	}()

	fakeWatcher.Add(&metav1.PartialObjectMetadata{})
	select {
	case <-eventHandled:
	case <-time.After(time.Second):
		t.Fatal("watch event was not handled")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watcher did not stop after cancellation")
	}
}

func TestRunWatcherLoopRetriesAfterClosedWatcher(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts atomic.Int32
	restarted := make(chan struct{})

	go runWatcherLoop(ctx, "test", func(context.Context) (watch.Interface, error) {
		if attempts.Add(1) == 2 {
			close(restarted)
		}
		watcher := watch.NewFake()
		watcher.Stop()
		return watcher, nil
	}, func(context.Context, watch.Event) {}, time.Millisecond, logrus.New())

	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("watcher was not restarted")
	}
	cancel()
	require.Eventually(t, func() bool { return attempts.Load() >= 2 }, time.Second, time.Millisecond)
	assert.GreaterOrEqual(t, attempts.Load(), int32(2))
}
