package main

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func TestProcessResourceMergesServiceMetadataAndItems(t *testing.T) {
	services := make(map[string]*HomerService)
	logger := logrus.New()
	processResource(map[string]string{homerServiceName: "Apps"}, &HomerItem{Name: "One", URL: "https://one.example.com"}, services, logger)
	processResource(map[string]string{
		homerServiceName: "Apps", homerServiceIcon: "fas fa-cloud", homerServiceRank: "2",
	}, &HomerItem{Name: "Two", URL: "https://two.example.com"}, services, logger)

	require.Len(t, services, 1)
	assert.Equal(t, "fas fa-cloud", services["Apps"].Icon)
	assert.Equal(t, 2, services["Apps"].Rank)
	assert.Len(t, services["Apps"].Items, 2)
}

func TestSortedServicesDropsEmptyServicesAndSortsItems(t *testing.T) {
	services := map[string]*HomerService{
		"empty": {Name: "Empty"},
		"apps": {
			Name:  "Apps",
			Items: []HomerItem{{Name: "Zulu"}, {Name: "Alpha"}},
		},
	}

	result := sortedServices(services)
	require.Len(t, result, 1)
	assert.Equal(t, []HomerItem{{Name: "Alpha"}, {Name: "Zulu"}}, result[0].Items)
}

func TestFetchHomerConfigCombinesIngressesAndIngressRoutes(t *testing.T) {
	client := fake.NewSimpleClientset(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "web.example.com"}}},
	})
	crdClient := apiextensionsfake.NewSimpleClientset(&apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: ingressRouteCRDName},
	})
	route := &IngressRoute{
		TypeMeta: metav1.TypeMeta{APIVersion: "traefik.io/v1alpha1", Kind: "IngressRoute"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "api", Namespace: "default",
			Annotations: map[string]string{homerServiceName: "APIs"},
		},
		Spec: IngressRouteSpec{Routes: []IngressRouteRoute{{Match: "Host(`api.example.com`)"}}},
	}
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), toUnstructured(t, route))

	config, err := fetchHomerConfig(context.Background(), client, crdClient, dynamicClient, []string{"default"}, logrus.New())
	require.NoError(t, err)
	require.Len(t, config.Services, 2)
	assert.Equal(t, "APIs", config.Services[0].Name)
	assert.Equal(t, "default", config.Services[1].Name)
	assert.Equal(t, "https://api.example.com", config.Services[0].Items[0].URL)
	assert.Equal(t, "https://web.example.com", config.Services[1].Items[0].URL)
}

func TestFetchHomerConfigSkipsIngressRoutesWithoutCRD(t *testing.T) {
	config, err := fetchHomerConfig(
		context.Background(), fake.NewSimpleClientset(), apiextensionsfake.NewSimpleClientset(),
		dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), []string{"default"}, logrus.New(),
	)
	require.NoError(t, err)
	assert.Empty(t, config.Services)
}

func TestFetchAllIngressesHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fetchAllIngresses(ctx, fake.NewSimpleClientset(), []string{"default"})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func toUnstructured(t *testing.T, object any) *unstructured.Unstructured {
	t.Helper()
	contents, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
	require.NoError(t, err)
	return &unstructured.Unstructured{Object: contents}
}
