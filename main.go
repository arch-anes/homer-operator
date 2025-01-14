package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/iancoleman/strcase"
	"github.com/sirupsen/logrus"
	traefikclientset "github.com/traefik/traefik/v3/pkg/provider/kubernetes/crd/generated/clientset/versioned"
	traefikv1alpha1 "github.com/traefik/traefik/v3/pkg/provider/kubernetes/crd/traefikio/v1alpha1"
	"gopkg.in/yaml.v3"
	networkingv1 "k8s.io/api/networking/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	configFilePath     = "/www/assets/config.yml"
	baseConfigFilePath = "/www/assets/base_config.yml"
	configSeparator    = "\n#Automatically generated config:\n"

	// Annotation selectors
	homerServiceName  = "homer.service.name"
	homerServiceIcon  = "homer.service.icon"
	homerServiceRank  = "homer.service.rank"
	homerItemName     = "homer.item.name"
	homerItemLogo     = "homer.item.logo"
	homerItemURL      = "homer.item.url"
	homerItemType     = "homer.item.type"
	homerItemExcluded = "homer.item.excluded"
	homerItemRank     = "homer.item.rank"

	// Default values
	defaultServiceName    = "default"
	defaultExclusionState = "false"
	defaultRank           = "0"
)

var log = logrus.New()

type HomerItem struct {
	Name     string `yaml:"name"`
	Logo     string `yaml:"logo"`
	URL      string `yaml:"url"`
	Type     string `yaml:"type"`
	Excluded bool   `yaml:"-"`
	Rank     int    `yaml:"-"`
}

type HomerService struct {
	Name  string      `yaml:"name"`
	Icon  string      `yaml:"icon"`
	Items []HomerItem `yaml:"items"`
	Rank  int         `yaml:"-"`
}

type HomerConfig struct {
	Services []HomerService `yaml:"services"`
}

type Operator struct {
	RequireAnnotation bool
	Clientset         kubernetes.Interface
	CRDClient         apiextensionsclientset.Interface
	TraefikClient     traefikclientset.Interface
}

func init() {
	log.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	log.SetLevel(logrus.InfoLevel)
}

func ignoreError[T any](val T, _ error) T {
	return val
}

func getAnnotationOrDefault(annotations map[string]string, key, defaultValue string) string {
	if val, exists := annotations[key]; exists {
		return val
	}
	return defaultValue
}

func (op *Operator) deduceURL(ingress networkingv1.Ingress) string {
	if len(ingress.Spec.Rules) > 0 {
		return "https://" + ingress.Spec.Rules[0].Host
	}
	return ""
}

func (op *Operator) deduceURLFromIngressRoute(ingressRoute traefikv1alpha1.IngressRoute) string {
	if len(ingressRoute.Spec.Routes) > 0 && len(ingressRoute.Spec.Routes[0].Match) > 0 {
		match := ingressRoute.Spec.Routes[0].Match
		if strings.HasPrefix(match, "Host(`") && strings.HasSuffix(match, "`)") {
			host := strings.TrimPrefix(match, "Host(`")
			host = strings.TrimSuffix(host, "`)")
			return "https://" + host
		}
	}
	return ""
}

func (op *Operator) extractHomerItemFromAnnotations(annotations map[string]string, name, url string, resourceName string, resourceType string) *HomerItem {
	if op.RequireAnnotation && !hasHomerAnnotations(annotations) {
		log.WithField(resourceType, resourceName).Info("No homer annotations found; skipping resource")
		return nil
	}

	item := &HomerItem{
		Name:     getAnnotationOrDefault(annotations, homerItemName, strcase.ToCamel(name)),
		Logo:     annotations[homerItemLogo],
		URL:      getAnnotationOrDefault(annotations, homerItemURL, url),
		Type:     annotations[homerItemType],
		Excluded: ignoreError(strconv.ParseBool(getAnnotationOrDefault(annotations, homerItemExcluded, defaultExclusionState))),
		Rank:     ignoreError(strconv.Atoi(getAnnotationOrDefault(annotations, homerItemRank, defaultRank))),
	}

	if item.Excluded {
		log.WithField(resourceType, resourceName).Info("Skipping excluded resource")
		return nil
	}

	if item.Name == "" || item.URL == "" {
		log.WithField(resourceType, resourceName).Warn("Skipping invalid resource")
		return nil
	}

	return item
}

func hasHomerAnnotations(annotations map[string]string) bool {
	keys := []string{
		homerServiceName,
		homerServiceIcon,
		homerServiceRank,
		homerItemName,
		homerItemLogo,
		homerItemURL,
		homerItemType,
		homerItemExcluded,
		homerItemRank,
	}
	for _, key := range keys {
		if _, exists := annotations[key]; exists {
			return true
		}
	}
	return false
}

func (op *Operator) extractHomerAnnotations(ingress networkingv1.Ingress) *HomerItem {
	return op.extractHomerItemFromAnnotations(
		ingress.Annotations,
		ingress.Name,
		op.deduceURL(ingress),
		ingress.Name,
		"ingress",
	)
}

func (op *Operator) extractHomerAnnotationsFromIngressRoute(ingressRoute traefikv1alpha1.IngressRoute) *HomerItem {
	return op.extractHomerItemFromAnnotations(
		ingressRoute.Annotations,
		ingressRoute.Name,
		op.deduceURLFromIngressRoute(ingressRoute),
		ingressRoute.Name,
		"ingressRoute",
	)
}

func sortByRankAndName[T interface {
	GetRank() int
	GetName() string
}](entries []T) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].GetRank() != 0 && entries[j].GetRank() != 0 {
			return entries[i].GetRank() < entries[j].GetRank()
		} else if entries[i].GetRank() != 0 {
			return true
		} else if entries[j].GetRank() != 0 {
			return false
		}
		return strings.ToLower(entries[i].GetName()) < strings.ToLower(entries[j].GetName())
	})
}

func (hi HomerItem) GetRank() int    { return hi.Rank }
func (hi HomerItem) GetName() string { return hi.Name }

func (hs HomerService) GetRank() int    { return hs.Rank }
func (hs HomerService) GetName() string { return hs.Name }

func (op *Operator) fetchAllIngresses() ([]networkingv1.Ingress, error) {
	var allIngresses []networkingv1.Ingress
	continueToken := ""

	for {
		options := metav1.ListOptions{Continue: continueToken}
		ingressList, err := op.Clientset.NetworkingV1().Ingresses("").List(context.TODO(), options)
		if err != nil {
			return nil, fmt.Errorf("failed to list ingresses: %w", err)
		}

		allIngresses = append(allIngresses, ingressList.Items...)
		if ingressList.Continue == "" {
			break
		}
		continueToken = ingressList.Continue
	}

	log.Infof("Total ingresses fetched: %d", len(allIngresses))
	return allIngresses, nil
}

func (op *Operator) fetchAllIngressRoutes() ([]traefikv1alpha1.IngressRoute, error) {
	var allIngressRoutes []traefikv1alpha1.IngressRoute
	continueToken := ""

	for {
		options := metav1.ListOptions{Continue: continueToken}
		ingressRouteList, err := op.TraefikClient.TraefikV1alpha1().IngressRoutes("").List(context.TODO(), options)
		if err != nil {
			return nil, fmt.Errorf("failed to list ingress routes: %w", err)
		}

		allIngressRoutes = append(allIngressRoutes, ingressRouteList.Items...)
		if ingressRouteList.Continue == "" {
			break
		}
		continueToken = ingressRouteList.Continue
	}

	log.Infof("Total ingress routes fetched: %d", len(allIngressRoutes))
	return allIngressRoutes, nil
}

func (op *Operator) checkCRDExists(crdName string) (bool, error) {
	_, err := op.CRDClient.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check CRD existence: %w", err)
	}
	return true, nil
}

func (op *Operator) fetchHomerConfig() (HomerConfig, error) {
	ingresses, err := op.fetchAllIngresses()
	if err != nil {
		return HomerConfig{}, fmt.Errorf("failed to fetch ingresses: %w", err)
	}

	ingressRoutes, err := op.fetchIngressRoutesIfCRDExists()
	if err != nil {
		return HomerConfig{}, fmt.Errorf("failed to fetch ingress routes: %w", err)
	}

	serviceMap := op.processResourcesToServiceMap(ingresses, ingressRoutes)
	services := op.convertServiceMapToSortedServices(serviceMap)

	return HomerConfig{Services: services}, nil
}

func (op *Operator) fetchIngressRoutesIfCRDExists() ([]traefikv1alpha1.IngressRoute, error) {
	crdExists, err := op.checkCRDExists("ingressroutes.traefik.io")
	if err != nil {
		return nil, fmt.Errorf("error checking CRD existence: %w", err)
	}
	if !crdExists {
		log.Warn("Traefik CRDs not found. Skipping IngressRoute processing.")
		return nil, nil
	}

	ingressRoutes, err := op.fetchAllIngressRoutes()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ingress routes: %w", err)
	}

	return ingressRoutes, nil
}

func (op *Operator) processResourcesToServiceMap(ingresses []networkingv1.Ingress, ingressRoutes []traefikv1alpha1.IngressRoute) map[string]*HomerService {
	serviceMap := make(map[string]*HomerService)

	for _, ingress := range ingresses {
		op.processIngress(ingress, serviceMap)
	}

	for _, ingressRoute := range ingressRoutes {
		op.processIngressRoute(ingressRoute, serviceMap)
	}

	return serviceMap
}

func (op *Operator) processResource(
	annotations map[string]string,
	item *HomerItem,
	serviceMap map[string]*HomerService,
) {
	if item == nil {
		return
	}

	service := &HomerService{
		Name:  getAnnotationOrDefault(annotations, homerServiceName, defaultServiceName),
		Icon:  annotations[homerServiceIcon],
		Items: []HomerItem{*item},
		Rank:  ignoreError(strconv.Atoi(getAnnotationOrDefault(annotations, homerServiceRank, defaultRank))),
	}

	op.updateServiceMap(serviceMap, service)
}

func (op *Operator) processIngress(ingress networkingv1.Ingress, serviceMap map[string]*HomerService) {
	log.WithField("ingress", ingress.Name).Debug("Processing ingress")
	item := op.extractHomerAnnotations(ingress)
	op.processResource(ingress.Annotations, item, serviceMap)
}

func (op *Operator) processIngressRoute(ingressRoute traefikv1alpha1.IngressRoute, serviceMap map[string]*HomerService) {
	log.WithField("ingressRoute", ingressRoute.Name).Debug("Processing ingress route")
	item := op.extractHomerAnnotationsFromIngressRoute(ingressRoute)
	op.processResource(ingressRoute.Annotations, item, serviceMap)
}

func (op *Operator) updateServiceMap(serviceMap map[string]*HomerService, service *HomerService) {
	if existingService, exists := serviceMap[service.Name]; exists {
		existingService.Items = append(existingService.Items, service.Items...)
		if existingService.Icon == "" && service.Icon != "" {
			existingService.Icon = service.Icon
		}
		if existingService.Rank == 0 && service.Rank != 0 {
			existingService.Rank = service.Rank
		}
	} else {
		serviceMap[service.Name] = service
	}
}

func (op *Operator) convertServiceMapToSortedServices(serviceMap map[string]*HomerService) []HomerService {
	var services []HomerService
	for _, service := range serviceMap {
		if len(service.Items) == 0 {
			log.WithField("service", service.Name).Warn("Skipping empty service")
			continue
		}
		sortByRankAndName(service.Items)
		services = append(services, *service)
	}
	sortByRankAndName(services)
	return services
}

func (op *Operator) mergeWithBaseConfig(generatedConfig []byte) ([]byte, error) {
	baseConfig, err := os.ReadFile(baseConfigFilePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read base config: %w", err)
	}
	return append(baseConfig, append([]byte(configSeparator), generatedConfig...)...), nil
}

func (op *Operator) writeToFile(config HomerConfig) error {
	yamlData, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	finalConfig, err := op.mergeWithBaseConfig(yamlData)
	if err != nil {
		return err
	}

	if err := os.WriteFile(configFilePath, finalConfig, 0644); err != nil {
		return fmt.Errorf("failed to write temporary YAML file: %w", err)
	}

	log.WithField("filePath", configFilePath).Info("YAML file updated")
	return nil
}

func (op *Operator) watchIngresses() {
	ingressWatcher, err := op.Clientset.NetworkingV1().Ingresses("").Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.WithError(err).Error("Failed to start watching ingresses")
		return
	}
	defer ingressWatcher.Stop()

	var ingressRouteWatcher watch.Interface
	crdExists, err := op.checkCRDExists("ingressroutes.traefik.io")
	if err != nil {
		log.WithError(err).Error("Error checking CRD existence")
		return
	}
	if crdExists {
		ingressRouteWatcher, err = op.TraefikClient.TraefikV1alpha1().IngressRoutes("").Watch(context.TODO(), metav1.ListOptions{})
		if err != nil {
			log.WithError(err).Error("Failed to start watching ingress routes")
			return
		}
		defer ingressRouteWatcher.Stop()
	} else {
		log.Warn("Traefik CRDs not found. Skipping IngressRoute watcher.")
	}

	log.Info("Watching for ingress and ingress route changes...")

	for {
		select {
		case event, ok := <-ingressWatcher.ResultChan():
			if !ok {
				log.Warn("Ingress watcher channel closed")
				return
			}
			if event.Type == watch.Added || event.Type == watch.Modified || event.Type == watch.Deleted {
				log.WithField("eventType", event.Type).Info("Ingress event detected")
				config, err := op.fetchHomerConfig()
				if err != nil {
					log.WithError(err).Error("Error fetching Homer config")
					continue
				}
				if err := op.writeToFile(config); err != nil {
					log.WithError(err).Error("Error writing to file")
				}
			} else {
				log.WithField("eventType", event.Type).Warn("Unhandled event type")
			}
		case event, ok := <-ingressRouteWatcher.ResultChan():
			if !ok {
				log.Warn("IngressRoute watcher channel closed")
				return
			}
			if event.Type == watch.Added || event.Type == watch.Modified || event.Type == watch.Deleted {
				log.WithField("eventType", event.Type).Info("IngressRoute event detected")
				config, err := op.fetchHomerConfig()
				if err != nil {
					log.WithError(err).Error("Error fetching Homer config")
					continue
				}
				if err := op.writeToFile(config); err != nil {
					log.WithError(err).Error("Error writing to file")
				}
			} else {
				log.WithField("eventType", event.Type).Warn("Unhandled event type")
			}
		}
	}
}

func (op *Operator) runPeriodicRefresh(stopCh <-chan struct{}) {
	wait.Until(func() {
		config, err := op.fetchHomerConfig()
		if err != nil {
			log.WithError(err).Error("Error during periodic refresh")
			return
		}
		if err := op.writeToFile(config); err != nil {
			log.WithError(err).Error("Error writing to file during periodic refresh")
		}
	}, 10*time.Minute, stopCh)
}

func main() {
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGINT, syscall.SIGTERM)

	stopStructCh := make(chan struct{})
	go func() {
		<-stopCh
		close(stopStructCh)
		log.Info("Shutting down...")
	}()

	config, err := rest.InClusterConfig()
	if err != nil {
		log.WithError(err).Error("Error loading in-cluster config")
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.WithError(err).Error("Error creating Kubernetes client")
		os.Exit(1)
	}

	crdClient, err := apiextensionsclientset.NewForConfig(config)
	if err != nil {
		log.WithError(err).Error("Error creating CRD client")
		os.Exit(1)
	}

	traefikClient, err := traefikclientset.NewForConfig(config)
	if err != nil {
		log.WithError(err).Error("Error creating Traefik client")
		os.Exit(1)
	}

	operator := &Operator{
		RequireAnnotation: true, // Set to true to enable the new mode
		Clientset:         clientset,
		CRDClient:         crdClient,
		TraefikClient:     traefikClient,
	}

	go operator.runPeriodicRefresh(stopStructCh)

	go operator.watchIngresses()

	<-stopStructCh
}
