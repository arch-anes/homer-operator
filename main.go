package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const refreshInterval = 10 * time.Minute

func main() {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.WithError(err).Error("Operator stopped")
		os.Exit(1)
	}
}

func run(ctx context.Context, logger logrus.FieldLogger) error {
	config, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("load in-cluster configuration: %w", err)
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	crdClient, err := apiextensionsclientset.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create CRD client: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	namespaces := parseWatchedNamespaces(os.Getenv("WATCHED_NAMESPACES"))
	logger.WithField("namespaces", displayNamespaces(namespaces)).Info("Starting Homer operator")
	reconciler := &reconciler{
		client: client, crdClient: crdClient, dynamicClient: dynamicClient,
		namespaces: namespaces, basePath: defaultBaseConfigFilePath,
		outputPath: defaultConfigFilePath, logger: logger,
	}
	reconcile := func(ctx context.Context) {
		if err := reconciler.reconcile(ctx); err != nil && ctx.Err() == nil {
			logger.WithError(err).Error("Failed to reconcile Homer config")
		}
	}

	reconcile(ctx)
	startWatchers(ctx, client, crdClient, dynamicClient, namespaces, reconcile, logger)

	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("Shutting down")
			return nil
		case <-ticker.C:
			reconcile(ctx)
		}
	}
}

func displayNamespaces(namespaces []string) any {
	if len(namespaces) == 1 && namespaces[0] == "" {
		return "all"
	}
	return namespaces
}
