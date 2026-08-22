package main

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

func fetchAllIngresses(ctx context.Context, client kubernetes.Interface, namespaces []string) ([]networkingv1.Ingress, error) {
	var ingresses []networkingv1.Ingress
	for _, namespace := range namespaces {
		continueToken := ""
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			list, err := client.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{Continue: continueToken})
			if err != nil {
				return nil, fmt.Errorf("list ingresses in namespace %q: %w", namespace, err)
			}
			ingresses = append(ingresses, list.Items...)
			continueToken = list.Continue
			if continueToken == "" {
				break
			}
		}
	}
	return ingresses, nil
}

func fetchAllIngressRoutes(ctx context.Context, client dynamic.Interface, namespaces []string) ([]IngressRoute, error) {
	var routes []IngressRoute
	for _, namespace := range namespaces {
		continueToken := ""
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			list, err := client.Resource(ingressRouteGVR).Namespace(namespace).List(ctx, metav1.ListOptions{Continue: continueToken})
			if err != nil {
				return nil, fmt.Errorf("list ingress routes in namespace %q: %w", namespace, err)
			}
			for _, item := range list.Items {
				var route IngressRoute
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, &route); err != nil {
					return nil, fmt.Errorf("decode ingress route %q in namespace %q: %w", item.GetName(), namespace, err)
				}
				routes = append(routes, route)
			}
			continueToken = list.GetContinue()
			if continueToken == "" {
				break
			}
		}
	}
	return routes, nil
}

func crdExists(ctx context.Context, client apiextensionsclientset.Interface, name string) (bool, error) {
	_, err := client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("get CRD %q: %w", name, err)
	}
	return true, nil
}

func fetchIngressRoutesIfAvailable(
	ctx context.Context,
	crdClient apiextensionsclientset.Interface,
	dynamicClient dynamic.Interface,
	namespaces []string,
) ([]IngressRoute, error) {
	exists, err := crdExists(ctx, crdClient, ingressRouteCRDName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	return fetchAllIngressRoutes(ctx, dynamicClient, namespaces)
}

func fetchHomerConfig(
	ctx context.Context,
	client kubernetes.Interface,
	crdClient apiextensionsclientset.Interface,
	dynamicClient dynamic.Interface,
	namespaces []string,
	logger logrus.FieldLogger,
) (HomerConfig, error) {
	ingresses, err := fetchAllIngresses(ctx, client, namespaces)
	if err != nil {
		return HomerConfig{}, err
	}
	routes, err := fetchIngressRoutesIfAvailable(ctx, crdClient, dynamicClient, namespaces)
	if err != nil {
		return HomerConfig{}, err
	}

	services := make(map[string]*HomerService)
	for _, ingress := range ingresses {
		resourceLogger := logger.WithFields(logrus.Fields{"kind": "Ingress", "namespace": ingress.Namespace, "name": ingress.Name})
		processResource(ingress.Annotations, extractHomerItem(ingress.Annotations, ingress.Name, deduceURL(ingress), resourceLogger), services, resourceLogger)
	}
	for _, route := range routes {
		resourceLogger := logger.WithFields(logrus.Fields{"kind": "IngressRoute", "namespace": route.Namespace, "name": route.Name})
		processResource(route.Annotations, extractHomerItem(route.Annotations, route.Name, deduceURLFromIngressRoute(route), resourceLogger), services, resourceLogger)
	}

	return HomerConfig{Services: sortedServices(services)}, nil
}

func processResource(annotations map[string]string, item *HomerItem, services map[string]*HomerService, logger logrus.FieldLogger) {
	if item == nil {
		return
	}
	service := &HomerService{
		Name:  annotationOrDefault(annotations, homerServiceName, defaultServiceName),
		Icon:  annotations[homerServiceIcon],
		Items: []HomerItem{*item},
		Rank:  parseAnnotationRank(annotations, homerServiceRank, logger),
	}
	if existing := services[service.Name]; existing != nil {
		existing.Items = append(existing.Items, service.Items...)
		if existing.Icon == "" {
			existing.Icon = service.Icon
		}
		if existing.Rank == 0 {
			existing.Rank = service.Rank
		}
		return
	}
	services[service.Name] = service
}

func sortedServices(serviceMap map[string]*HomerService) []HomerService {
	services := make([]HomerService, 0, len(serviceMap))
	for _, service := range serviceMap {
		if len(service.Items) == 0 {
			continue
		}
		sortByRankAndName(service.Items)
		services = append(services, *service)
	}
	sortByRankAndName(services)
	return services
}
