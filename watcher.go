package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

type reconciler struct {
	client        kubernetes.Interface
	crdClient     apiextensionsclientset.Interface
	dynamicClient dynamic.Interface
	namespaces    []string
	basePath      string
	outputPath    string
	logger        logrus.FieldLogger
	mu            sync.Mutex
}

func (r *reconciler) reconcile(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	config, err := fetchHomerConfig(ctx, r.client, r.crdClient, r.dynamicClient, r.namespaces, r.logger)
	if err != nil {
		return fmt.Errorf("fetch resources: %w", err)
	}
	if err := writeConfig(config, r.basePath, r.outputPath); err != nil {
		return err
	}
	r.logger.WithField("path", r.outputPath).Info("Homer config updated")
	return nil
}

func runWatcherLoop(
	ctx context.Context,
	resourceName string,
	createWatcher func(context.Context) (watch.Interface, error),
	handleEvent func(context.Context, watch.Event),
	retryDelay time.Duration,
	logger logrus.FieldLogger,
) {
	for ctx.Err() == nil {
		watcher, err := createWatcher(ctx)
		if err != nil {
			logger.WithError(err).WithField("resource", resourceName).Error("Failed to create watcher")
			if !waitForContext(ctx, retryDelay) {
				return
			}
			continue
		}
		if watcher == nil {
			if !waitForContext(ctx, retryDelay) {
				return
			}
			continue
		}

		logger.WithField("resource", resourceName).Info("Watcher started")
		closed := false
		for !closed {
			select {
			case <-ctx.Done():
				watcher.Stop()
				return
			case event, ok := <-watcher.ResultChan():
				if !ok {
					closed = true
					continue
				}
				if event.Type == watch.Error {
					logger.WithField("resource", resourceName).Warn("Watcher reported an error; restarting")
					closed = true
					continue
				}
				handleEvent(ctx, event)
			}
		}
		watcher.Stop()
		if !waitForContext(ctx, retryDelay) {
			return
		}
	}
}

func waitForContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func startWatchers(
	ctx context.Context,
	client kubernetes.Interface,
	crdClient apiextensionsclientset.Interface,
	dynamicClient dynamic.Interface,
	namespaces []string,
	onChange func(context.Context),
	logger logrus.FieldLogger,
) {
	for _, namespace := range namespaces {
		namespace := namespace
		go runWatcherLoop(ctx, "Ingress("+namespace+")", func(ctx context.Context) (watch.Interface, error) {
			return client.NetworkingV1().Ingresses(namespace).Watch(ctx, metav1.ListOptions{})
		}, func(ctx context.Context, _ watch.Event) {
			onChange(ctx)
		}, 5*time.Second, logger)

		go runWatcherLoop(ctx, "IngressRoute("+namespace+")", func(ctx context.Context) (watch.Interface, error) {
			exists, err := crdExists(ctx, crdClient, ingressRouteCRDName)
			if err != nil || !exists {
				return nil, err
			}
			return dynamicClient.Resource(ingressRouteGVR).Namespace(namespace).Watch(ctx, metav1.ListOptions{})
		}, func(ctx context.Context, _ watch.Event) {
			onChange(ctx)
		}, 5*time.Second, logger)
	}
}
