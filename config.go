package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/iancoleman/strcase"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	networkingv1 "k8s.io/api/networking/v1"
)

func parseWatchedNamespaces(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{""}
	}

	seen := make(map[string]struct{})
	namespaces := make([]string, 0)
	for _, namespace := range strings.Split(value, ",") {
		namespace = strings.TrimSpace(namespace)
		if namespace == "" {
			continue
		}
		if _, exists := seen[namespace]; exists {
			continue
		}
		seen[namespace] = struct{}{}
		namespaces = append(namespaces, namespace)
	}

	if len(namespaces) == 0 {
		return []string{""}
	}
	sort.Strings(namespaces)
	return namespaces
}

func annotationOrDefault(annotations map[string]string, key, fallback string) string {
	if value, exists := annotations[key]; exists {
		return value
	}
	return fallback
}

func parseAnnotationBool(annotations map[string]string, key string, logger logrus.FieldLogger) bool {
	value := annotations[key]
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		logger.WithFields(logrus.Fields{"annotation": key, "value": value}).Warn("Ignoring invalid boolean annotation")
		return false
	}
	return parsed
}

func parseAnnotationRank(annotations map[string]string, key string, logger logrus.FieldLogger) int {
	value := annotations[key]
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		logger.WithFields(logrus.Fields{"annotation": key, "value": value}).Warn("Ignoring invalid rank annotation")
		return 0
	}
	return parsed
}

func deduceURL(ingress networkingv1.Ingress) string {
	for _, rule := range ingress.Spec.Rules {
		if rule.Host != "" {
			return "https://" + rule.Host
		}
	}
	return ""
}

func deduceURLFromIngressRoute(ingressRoute IngressRoute) string {
	for _, route := range ingressRoute.Spec.Routes {
		const prefix, suffix = "Host(`", "`)"
		if strings.HasPrefix(route.Match, prefix) && strings.HasSuffix(route.Match, suffix) {
			host := strings.TrimSuffix(strings.TrimPrefix(route.Match, prefix), suffix)
			if host != "" {
				return "https://" + host
			}
		}
	}
	return ""
}

func extractHomerItem(
	annotations map[string]string,
	name string,
	deducedURL string,
	logger logrus.FieldLogger,
) *HomerItem {
	if parseAnnotationBool(annotations, homerItemExcluded, logger) {
		logger.Info("Skipping excluded resource")
		return nil
	}

	item := &HomerItem{
		Name: annotationOrDefault(annotations, homerItemName, strcase.ToCamel(name)),
		Logo: annotations[homerItemLogo],
		URL:  annotationOrDefault(annotations, homerItemURL, deducedURL),
		Type: annotations[homerItemType],
		Rank: parseAnnotationRank(annotations, homerItemRank, logger),
	}
	if item.Name == "" || item.URL == "" {
		logger.Warn("Skipping resource without a name or URL")
		return nil
	}
	return item
}

func mergeWithBaseConfig(baseConfig, generatedConfig []byte) []byte {
	merged := make([]byte, 0, len(baseConfig)+len(configSeparator)+len(generatedConfig))
	merged = append(merged, baseConfig...)
	merged = append(merged, configSeparator...)
	merged = append(merged, generatedConfig...)
	return merged
}

func renderConfig(config HomerConfig, baseConfig []byte) ([]byte, error) {
	generatedConfig, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal generated config: %w", err)
	}
	return mergeWithBaseConfig(baseConfig, generatedConfig), nil
}

func writeConfig(config HomerConfig, basePath, outputPath string) error {
	baseConfig, err := os.ReadFile(basePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read base config: %w", err)
	}

	contents, err := renderConfig(config, baseConfig)
	if err != nil {
		return err
	}

	outputDirectory := filepath.Dir(outputPath)
	temporaryFile, err := os.CreateTemp(outputDirectory, ".homer-config-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	temporaryPath := temporaryFile.Name()
	defer os.Remove(temporaryPath)

	if err := temporaryFile.Chmod(0o644); err != nil {
		temporaryFile.Close()
		return fmt.Errorf("set config permissions: %w", err)
	}
	if _, err := temporaryFile.Write(contents); err != nil {
		temporaryFile.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temporaryFile.Sync(); err != nil {
		temporaryFile.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := temporaryFile.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
