package main

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	traefikfake "github.com/traefik/traefik/v3/pkg/provider/kubernetes/crd/generated/clientset/versioned/fake"
	traefikv1alpha1 "github.com/traefik/traefik/v3/pkg/provider/kubernetes/crd/traefikio/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestGetAnnotationOrDefault(t *testing.T) {
	annotations := map[string]string{
		"key1": "value1",
	}

	assert.Equal(t, "value1", getAnnotationOrDefault(annotations, "key1", "default"))

	assert.Equal(t, "default", getAnnotationOrDefault(annotations, "key2", "default"))
}

func TestDeduceURL(t *testing.T) {
	ingress := networkingv1.Ingress{
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
				},
			},
		},
	}

	assert.Equal(t, "https://example.com", deduceURL(ingress))

	ingress.Spec.Rules = []networkingv1.IngressRule{}
	assert.Equal(t, "", deduceURL(ingress))
}

func TestDeduceURLFromIngressRoute(t *testing.T) {
	ingressRoute := traefikv1alpha1.IngressRoute{
		Spec: traefikv1alpha1.IngressRouteSpec{
			Routes: []traefikv1alpha1.Route{
				{
					Match: "Host(`example.com`)",
				},
			},
		},
	}

	assert.Equal(t, "https://example.com", deduceURLFromIngressRoute(ingressRoute))

	ingressRoute.Spec.Routes = []traefikv1alpha1.Route{}
	assert.Equal(t, "", deduceURLFromIngressRoute(ingressRoute))
}

func TestExtractHomerItemFromAnnotations(t *testing.T) {
	annotations := map[string]string{
		homerItemName:     "TestItem",
		homerItemLogo:     "logo.png",
		homerItemURL:      "https://example.com",
		homerItemType:     "type",
		homerItemExcluded: "false",
		homerItemRank:     "5",
	}

	item := extractHomerItemFromAnnotations(annotations, "TestItem", "https://example.com", "TestItem", "ingress")
	assert.Equal(t, "TestItem", item.Name)
	assert.Equal(t, "logo.png", item.Logo)
	assert.Equal(t, "https://example.com", item.URL)
	assert.Equal(t, "type", item.Type)
	assert.Equal(t, false, item.Excluded)
	assert.Equal(t, 5, item.Rank)

	annotations[homerItemExcluded] = "true"
	item = extractHomerItemFromAnnotations(annotations, "TestItem", "https://example.com", "TestItem", "ingress")
	assert.Nil(t, item)

	annotations[homerItemName] = ""
	annotations[homerItemURL] = ""
	item = extractHomerItemFromAnnotations(annotations, "TestItem", "https://example.com", "TestItem", "ingress")
	assert.Nil(t, item)
}

func TestSortByRankAndName(t *testing.T) {
	items := []HomerItem{
		{Name: "B", Rank: 2},
		{Name: "A", Rank: 1},
		{Name: "C", Rank: 0},
	}

	sortByRankAndName(items)
	assert.Equal(t, "A", items[0].Name)
	assert.Equal(t, "B", items[1].Name)
	assert.Equal(t, "C", items[2].Name)
}

func TestUpdateServiceMap(t *testing.T) {
	serviceMap := make(map[string]*HomerService)

	service := &HomerService{
		Name:  "Service1",
		Icon:  "icon1",
		Items: []HomerItem{{Name: "Item1"}},
		Rank:  1,
	}
	updateServiceMap(serviceMap, service)
	assert.Equal(t, 1, len(serviceMap))
	assert.Equal(t, "Service1", serviceMap["Service1"].Name)

	service = &HomerService{
		Name:  "Service1",
		Icon:  "icon2",
		Items: []HomerItem{{Name: "Item2"}},
		Rank:  2,
	}
	updateServiceMap(serviceMap, service)
	assert.Equal(t, 1, len(serviceMap))
	assert.Equal(t, "icon1", serviceMap["Service1"].Icon)
	assert.Equal(t, 2, len(serviceMap["Service1"].Items))
}

func TestConvertServiceMapToSortedServices(t *testing.T) {
	serviceMap := map[string]*HomerService{
		"Service2": {Name: "Service2", Rank: 2},
		"Service1": {Name: "Service1", Rank: 1},
	}

	services := convertServiceMapToSortedServices(serviceMap)
	assert.Equal(t, 0, len(services))
}

func TestMergeWithBaseConfig(t *testing.T) {
	generatedConfig := []byte("generated config\n")

	mergedConfig, err := mergeWithBaseConfig(generatedConfig)
	assert.NoError(t, err)
	assert.Equal(t, "\n#Automatically generated config:\ngenerated config\n", string(mergedConfig))

	mergedConfig, err = mergeWithBaseConfig(generatedConfig)
	assert.NoError(t, err)
	assert.Equal(t, "\n#Automatically generated config:\ngenerated config\n", string(mergedConfig))
}

func TestFetchAllIngresses(t *testing.T) {
	mockClient := fake.NewSimpleClientset(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ingress",
		},
	})

	ingresses, err := fetchAllIngresses(mockClient)
	assert.NoError(t, err)
	assert.Len(t, ingresses, 1)
	assert.Equal(t, "test-ingress", ingresses[0].Name)
}

func TestProcessIngress(t *testing.T) {
	ingress := networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ingress",
			Annotations: map[string]string{
				homerItemName: "TestItem",
				homerItemURL:  "https://example.com",
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
				},
			},
		},
	}

	serviceMap := make(map[string]*HomerService)
	processIngress(ingress, serviceMap)

	assert.Len(t, serviceMap, 1)
	assert.Equal(t, "default", serviceMap["default"].Name)
	assert.Len(t, serviceMap["default"].Items, 1)
	assert.Equal(t, "TestItem", serviceMap["default"].Items[0].Name)
}

func TestIgnoreError(t *testing.T) {
	value := ignoreError(strconv.Atoi("42"))
	assert.Equal(t, 42, value)

	value = ignoreError(strconv.Atoi("invalid"))
	assert.Equal(t, 0, value)
}

func TestFetchHomerConfig(t *testing.T) {
	mockClient := fake.NewSimpleClientset(&networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ingress",
			Annotations: map[string]string{
				homerItemName: "TestItem",
				homerItemURL:  "https://example.com",
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
				},
			},
		},
	})

	crdClient := apiextensionsfake.NewSimpleClientset()
	traefikClient := traefikfake.NewSimpleClientset()

	config, err := fetchHomerConfig(mockClient, crdClient, traefikClient)
	assert.NoError(t, err)
	assert.Len(t, config.Services, 1)
	assert.Equal(t, "default", config.Services[0].Name)
	assert.Len(t, config.Services[0].Items, 1)
	assert.Equal(t, "TestItem", config.Services[0].Items[0].Name)
}
