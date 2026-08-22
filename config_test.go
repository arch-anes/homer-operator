package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	networkingv1 "k8s.io/api/networking/v1"
)

func TestParseWatchedNamespaces(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{name: "empty means all", want: []string{""}},
		{name: "whitespace means all", value: " , ", want: []string{""}},
		{name: "normalizes values", value: " kube-system,default,kube-system ", want: []string{"default", "kube-system"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, parseWatchedNamespaces(test.value))
		})
	}
}

func TestDeduceURLUsesFirstNonEmptyHost(t *testing.T) {
	ingress := networkingv1.Ingress{Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{
		{Host: ""},
		{Host: "example.com"},
	}}}
	assert.Equal(t, "https://example.com", deduceURL(ingress))
}

func TestDeduceURLFromIngressRouteUsesFirstValidRoute(t *testing.T) {
	route := IngressRoute{Spec: IngressRouteSpec{Routes: []IngressRouteRoute{
		{Match: "PathPrefix(`/test`)"},
		{Match: "Host(`example.com`)"},
	}}}
	assert.Equal(t, "https://example.com", deduceURLFromIngressRoute(route))
}

func TestExtractHomerItem(t *testing.T) {
	logger := logrus.New()
	annotations := map[string]string{
		homerItemName: "Status", homerItemLogo: "logo.svg",
		homerItemURL: "https://status.example.com", homerItemType: "Ping",
		homerItemRank: "5",
	}

	item := extractHomerItem(annotations, "status-page", "https://fallback.example.com", logger)
	require.NotNil(t, item)
	assert.Equal(t, &HomerItem{
		Name: "Status", Logo: "logo.svg", URL: "https://status.example.com",
		Type: "Ping", Rank: 5,
	}, item)

	annotations[homerItemExcluded] = "true"
	assert.Nil(t, extractHomerItem(annotations, "status-page", "https://fallback.example.com", logger))
}

func TestExtractHomerItemRejectsMissingURL(t *testing.T) {
	assert.Nil(t, extractHomerItem(nil, "no-host", "", logrus.New()))
}

func TestInvalidAnnotationValuesUseDefaults(t *testing.T) {
	logger := logrus.New()
	annotations := map[string]string{homerItemExcluded: "sometimes", homerItemRank: "first"}
	item := extractHomerItem(annotations, "example", "https://example.com", logger)
	require.NotNil(t, item)
	assert.Zero(t, item.Rank)
}

func TestSortByRankAndName(t *testing.T) {
	items := []HomerItem{
		{Name: "Zulu"},
		{Name: "Beta", Rank: 2},
		{Name: "Alpha", Rank: 2},
		{Name: "Charlie", Rank: 1},
	}

	sortByRankAndName(items)
	assert.Equal(t, []string{"Charlie", "Alpha", "Beta", "Zulu"}, []string{
		items[0].Name, items[1].Name, items[2].Name, items[3].Name,
	})
}

func TestRenderConfigOmitsEmptyOptionalFields(t *testing.T) {
	contents, err := renderConfig(HomerConfig{Services: []HomerService{{
		Name: "Apps", Items: []HomerItem{{Name: "Example", URL: "https://example.com"}},
	}}}, []byte("title: Home\n"))
	require.NoError(t, err)
	assert.Equal(t, "title: Home\n\n# Automatically generated config:\nservices:\n    - name: Apps\n      items:\n        - name: Example\n          url: https://example.com\n", string(contents))
}

func TestWriteConfigReplacesOutput(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "base.yml")
	outputPath := filepath.Join(directory, "config.yml")
	require.NoError(t, os.WriteFile(basePath, []byte("title: Home\n"), 0o644))
	require.NoError(t, os.WriteFile(outputPath, []byte("stale"), 0o644))

	err := writeConfig(HomerConfig{}, basePath, outputPath)
	require.NoError(t, err)
	contents, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Equal(t, "title: Home\n\n# Automatically generated config:\nservices: []\n", string(contents))
	matches, err := filepath.Glob(filepath.Join(directory, ".homer-config-*"))
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestWriteConfigReportsInvalidOutputDirectory(t *testing.T) {
	err := writeConfig(HomerConfig{}, filepath.Join(t.TempDir(), "missing-base.yml"), filepath.Join(t.TempDir(), "missing", "config.yml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create temporary config")
}
