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

	operator := &Operator{RequireAnnotation: false}

	assert.Equal(t, "value1", operator.getAnnotationOrDefault(annotations, "key1", "default"))

	assert.Equal(t, "default", operator.getAnnotationOrDefault(annotations, "key2", "default"))
}

func TestDeduceURL(t *testing.T) {
	operator := &Operator{}

	ingress := networkingv1.Ingress{
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
				},
			},
		},
	}

	assert.Equal(t, "https://example.com", operator.deduceURL(ingress))

	ingress.Spec.Rules = []networkingv1.IngressRule{}
	assert.Equal(t, "", operator.deduceURL(ingress))
}

func TestDeduceURLFromIngressRoute(t *testing.T) {
	operator := &Operator{}

	ingressRoute := traefikv1alpha1.IngressRoute{
		Spec: traefikv1alpha1.IngressRouteSpec{
			Routes: []traefikv1alpha1.Route{
				{
					Match: "Host(`example.com`)",
				},
			},
		},
	}

	assert.Equal(t, "https://example.com", operator.deduceURLFromIngressRoute(ingressRoute))

	ingressRoute.Spec.Routes = []traefikv1alpha1.Route{}
	assert.Equal(t, "", operator.deduceURLFromIngressRoute(ingressRoute))
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

	operator := &Operator{RequireAnnotation: false}

	item := operator.extractHomerItemFromAnnotations(annotations, "TestItem", "https://example.com", "TestItem", "ingress")
	assert.Equal(t, "TestItem", item.Name)
	assert.Equal(t, "logo.png", item.Logo)
	assert.Equal(t, "https://example.com", item.URL)
	assert.Equal(t, "type", item.Type)
	assert.Equal(t, false, item.Excluded)
	assert.Equal(t, 5, item.Rank)

	annotations[homerItemExcluded] = "true"
	item = operator.extractHomerItemFromAnnotations(annotations, "TestItem", "https://example.com", "TestItem", "ingress")
	assert.Nil(t, item)

	annotations[homerItemName] = ""
	annotations[homerItemURL] = ""
	item = operator.extractHomerItemFromAnnotations(annotations, "TestItem", "https://example.com", "TestItem", "ingress")
	assert.Nil(t, item)
}

func TestExtractHomerItemFromAnnotations_WithRequireAnnotation(t *testing.T) {
	annotations := map[string]string{
		homerItemName:     "TestItem",
		homerItemLogo:     "logo.png",
		homerItemURL:      "https://example.com",
		homerItemType:     "type",
		homerItemExcluded: "false",
		homerItemRank:     "5",
	}

	operator := &Operator{RequireAnnotation: true}

	item := operator.extractHomerItemFromAnnotations(annotations, "TestItem", "https://example.com", "TestItem", "ingress")
	assert.Equal(t, "TestItem", item.Name)
}

func TestExtractHomerItemFromAnnotations_WithoutAnnotations(t *testing.T) {
	annotations := map[string]string{}

	operator := &Operator{RequireAnnotation: true}

	item := operator.extractHomerItemFromAnnotations(annotations, "TestItem", "https://example.com", "TestItem", "ingress")
	assert.Nil(t, item)
}

func TestSortByRankAndName(t *testing.T) {
	operator := &Operator{}

	items := []HomerItem{
		{Name: "B", Rank: 2},
		{Name: "A", Rank: 1},
		{Name: "C", Rank: 0},
	}

	operator.sortByRankAndName(items)
	assert.Equal(t, "A", items[0].Name)
	assert.Equal(t, "B", items[1].Name)
	assert.Equal(t, "C", items[2].Name)
}

func TestUpdateServiceMap(t *testing.T) {
	operator := &Operator{}

	serviceMap := make(map[string]*HomerService)

	service := &HomerService{
		Name:  "Service1",
		Icon:  "icon1",
		Items: []HomerItem{{Name: "Item1"}},
		Rank:  1,
	}
	operator.updateServiceMap(serviceMap, service)
	assert.Equal(t, 1, len(serviceMap))
	assert.Equal(t, "Service1", serviceMap["Service1"].Name)

	service = &HomerService{
		Name:  "Service1",
		Icon:  "icon2",
		Items: []HomerItem{{Name: "Item2"}},
		Rank:  2,
	}
	operator.updateServiceMap(serviceMap, service)
	assert.Equal(t, 1, len(serviceMap))
	assert.Equal(t, "icon1", serviceMap["Service1"].Icon)
	assert.Equal(t, 2, len(serviceMap["Service1"].Items))
}

func TestConvertServiceMapToSortedServices(t *testing.T) {
	operator := &Operator{}

	serviceMap := map[string]*HomerService{
		"Service2": {Name: "Service2", Rank: 2},
		"Service1": {Name: "Service1", Rank: 1},
	}

	services := operator.convertServiceMapToSortedServices(serviceMap)
	assert.Len(t, services, 2)
	assert.Equal(t, "Service1", services[0].Name)
	assert.Equal(t, "Service2", services[1].Name)
}

func TestMergeWithBaseConfig(t *testing.T) {
	operator := &Operator{}

	// Mock os.ReadFile by overriding the method if necessary
	// For simplicity, this test assumes the base config does not exist

	generatedConfig := []byte("generated config\n")

	finalConfig, err := operator.mergeWithBaseConfig(generatedConfig)
	assert.NoError(t, err)
	assert.Equal(t, "\n#Automatically generated config:\ngenerated config\n", string(finalConfig))
}

func TestFetchAllIngresses(t *testing.T) {
	operator := &Operator{
		Clientset: fake.NewSimpleClientset(&networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-ingress",
			},
		}),
	}

	ingresses, err := operator.fetchAllIngresses()
	assert.NoError(t, err)
	assert.Len(t, ingresses, 1)
	assert.Equal(t, "test-ingress", ingresses[0].Name)
}

func TestProcessIngress(t *testing.T) {
	operator := &Operator{
		RequireAnnotation: false,
	}

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
	operator.processIngress(ingress, serviceMap)

	assert.Len(t, serviceMap, 1)
	assert.Equal(t, "default", serviceMap["default"].Name)
	assert.Len(t, serviceMap["default"].Items, 1)
	assert.Equal(t, "TestItem", serviceMap["default"].Items[0].Name)
}

func TestIgnoreError(t *testing.T) {
	operator := &Operator{}

	value := operator.ignoreError(strconv.Atoi("42"))
	assert.Equal(t, 42, value)

	value = operator.ignoreError(strconv.Atoi("invalid"))
	assert.Equal(t, 0, value)
}

func TestFetchHomerConfig(t *testing.T) {
	operator := &Operator{
		Clientset:     fake.NewSimpleClientset(&networkingv1.Ingress{
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
		}),
		CRDClient:     apiextensionsfake.NewSimpleClientset(),
		TraefikClient: traefikfake.NewSimpleClientset(),
	}

	config, err := operator.fetchHomerConfig()
	assert.NoError(t, err)
	assert.Len(t, config.Services, 1)
	assert.Equal(t, "default", config.Services[0].Name)
	assert.Len(t, config.Services[0].Items, 1)
	assert.Equal(t, "TestItem", config.Services[0].Items[0].Name)
}
