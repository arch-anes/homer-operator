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

var watchedNamespaces = parseWatchedNamespaces()

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

func init() {
	log.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})
	log.SetLevel(logrus.InfoLevel)
}

func parseWatchedNamespaces() []string {
	v := os.Getenv("WATCHED_NAMESPACES")
	if v == "" {
		return []string{""}
	}
	seen := map[string]struct{}{}
	var out []string
	for _, ns := range strings.Split(v, ",") {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			continue
		}
		if _, ok := seen[ns]; ok {
			continue
		}
		seen[ns] = struct{}{}
		out = append(out, ns)
	}
	sort.Strings(out)
	log.WithField("namespaces", out).Info("Namespace watchlist active")
	return out
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

func deduceURL(ingress networkingv1.Ingress) string {
	if len(ingress.Spec.Rules) > 0 {
		return "https://" + ingress.Spec.Rules[0].Host
	}
	return ""
}

func deduceURLFromIngressRoute(ingressRoute traefikv1alpha1.IngressRoute) string {
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

func extractHomerItemFromAnnotations(annotations map[string]string, name, url string, resourceName string, resourceType string) *HomerItem {
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

func extractHomerAnnotations(ingress networkingv1.Ingress) *HomerItem {
	return extractHomerItemFromAnnotations(
		ingress.Annotations,
		ingress.Name,
		deduceURL(ingress),
		ingress.Name,
		"ingress",
	)
}

func extractHomerAnnotationsFromIngressRoute(ingressRoute traefikv1alpha1.IngressRoute) *HomerItem {
	return extractHomerItemFromAnnotations(
		ingressRoute.Annotations,
		ingressRoute.Name,
		deduceURLFromIngressRoute(ingressRoute),
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

func fetchAllIngresses(clientset kubernetes.Interface) ([]networkingv1.Ingress, error) {
	var allIngresses []networkingv1.Ingress

	for _, ns := range watchedNamespaces {
		continueToken := ""
		for {
			options := metav1.ListOptions{Continue: continueToken}
			ingressList, err := clientset.NetworkingV1().Ingresses(ns).List(context.TODO(), options)
			if err != nil {
				return nil, fmt.Errorf("failed to list ingresses in %q: %w", ns, err)
			}

			allIngresses = append(allIngresses, ingressList.Items...)
			if ingressList.Continue == "" {
				break
			}
			continueToken = ingressList.Continue
		}
	}

	log.Infof("Total ingresses fetched: %d", len(allIngresses))
	return allIngresses, nil
}

func fetchAllIngressRoutes(traefikClient traefikclientset.Interface) ([]traefikv1alpha1.IngressRoute, error) {
	var allIngressRoutes []traefikv1alpha1.IngressRoute

	for _, ns := range watchedNamespaces {
		continueToken := ""

		for {
			options := metav1.ListOptions{Continue: continueToken}
			ingressRouteList, err := traefikClient.TraefikV1alpha1().IngressRoutes(ns).List(context.TODO(), options)
			if err != nil {
				return nil, fmt.Errorf("failed to list ingress routes in %q: %w", ns, err)
			}

			allIngressRoutes = append(allIngressRoutes, ingressRouteList.Items...)
			if ingressRouteList.Continue == "" {
				break
			}
			continueToken = ingressRouteList.Continue
		}
	}

	log.Infof("Total ingress routes fetched: %d", len(allIngressRoutes))
	return allIngressRoutes, nil
}

func checkCRDExists(clientset apiextensionsclientset.Interface, crdName string) (bool, error) {
	_, err := clientset.ApiextensionsV1().CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check CRD existence: %w", err)
	}
	return true, nil
}

func fetchHomerConfig(clientset kubernetes.Interface, crdClient apiextensionsclientset.Interface, traefikClient traefikclientset.Interface) (HomerConfig, error) {
	ingresses, err := fetchAllIngresses(clientset)
	if err != nil {
		return HomerConfig{}, fmt.Errorf("failed to fetch ingresses: %w", err)
	}

	ingressRoutes, err := fetchIngressRoutesIfCRDExists(crdClient, traefikClient)
	if err != nil {
		return HomerConfig{}, fmt.Errorf("failed to fetch ingress routes: %w", err)
	}

	serviceMap := processResourcesToServiceMap(ingresses, ingressRoutes)
	services := convertServiceMapToSortedServices(serviceMap)

	return HomerConfig{Services: services}, nil
}

func fetchIngressRoutesIfCRDExists(crdClient apiextensionsclientset.Interface, traefikClient traefikclientset.Interface) ([]traefikv1alpha1.IngressRoute, error) {
	crdExists, err := checkCRDExists(crdClient, "ingressroutes.traefik.io")
	if err != nil {
		return nil, fmt.Errorf("error checking CRD existence: %w", err)
	}
	if !crdExists {
		log.Warn("Traefik CRDs not found. Skipping IngressRoute processing.")
		return nil, nil
	}

	ingressRoutes, err := fetchAllIngressRoutes(traefikClient)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ingress routes: %w", err)
	}

	return ingressRoutes, nil
}

func processResourcesToServiceMap(ingresses []networkingv1.Ingress, ingressRoutes []traefikv1alpha1.IngressRoute) map[string]*HomerService {
	serviceMap := make(map[string]*HomerService)

	for _, ingress := range ingresses {
		processIngress(ingress, serviceMap)
	}

	for _, ingressRoute := range ingressRoutes {
		processIngressRoute(ingressRoute, serviceMap)
	}

	return serviceMap
}

func processResource(
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

	updateServiceMap(serviceMap, service)
}

func processIngress(ingress networkingv1.Ingress, serviceMap map[string]*HomerService) {
	log.WithField("ingress", ingress.Name).Debug("Processing ingress")
	item := extractHomerAnnotations(ingress)
	processResource(ingress.Annotations, item, serviceMap)
}

func processIngressRoute(ingressRoute traefikv1alpha1.IngressRoute, serviceMap map[string]*HomerService) {
	log.WithField("ingressRoute", ingressRoute.Name).Debug("Processing ingress route")
	item := extractHomerAnnotationsFromIngressRoute(ingressRoute)
	processResource(ingressRoute.Annotations, item, serviceMap)
}

func updateServiceMap(serviceMap map[string]*HomerService, service *HomerService) {
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

func convertServiceMapToSortedServices(serviceMap map[string]*HomerService) []HomerService {
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

func mergeWithBaseConfig(generatedConfig []byte) ([]byte, error) {
	baseConfig, err := os.ReadFile(baseConfigFilePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read base config: %w", err)
	}
	return append(baseConfig, append([]byte(configSeparator), generatedConfig...)...), nil
}

func writeToFile(config HomerConfig) error {
	yamlData, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	finalConfig, err := mergeWithBaseConfig(yamlData)
	if err != nil {
		return err
	}

	if err := os.WriteFile(configFilePath, finalConfig, 0644); err != nil {
		return fmt.Errorf("failed to write temporary YAML file: %w", err)
	}

	log.WithField("filePath", configFilePath).Info("YAML file updated")
	return nil
}

func runWatcherLoop(
	resourceName string,
	createWatcher func() (watch.Interface, error),
	handleEvent func(resourceName string, event watch.Event),
	stopCh <-chan struct{},
) {
	go func() {
		for {
			select {
			case <-stopCh:
				log.Infof("%s watcher shutting down", resourceName)
				return
			default:
			}

			watcher, err := createWatcher()
			if err != nil {
				log.WithError(err).Errorf("Failed to create %s watcher, retrying in 5s", resourceName)
				time.Sleep(5 * time.Second)
				continue
			}

			if watcher == nil {
				log.Debugf("Watcher for %s not instantiated", resourceName)
				time.Sleep(5 * time.Second)
				continue
			}

			log.Infof("Started %s watcher", resourceName)
			for event := range watcher.ResultChan() {
				if event.Type == "" || event.Object == nil {
					log.Warnf("%s watcher channel closed, restarting", resourceName)
					break
				}
				handleEvent(resourceName, event)
			}

			watcher.Stop()
			time.Sleep(2 * time.Second)
		}
	}()
}

func watchIngresses(
	clientset kubernetes.Interface,
	crdClient apiextensionsclientset.Interface,
	traefikClient traefikclientset.Interface,
	stopCh <-chan struct{},
) {
	eventHandler := func(resourceName string, event watch.Event) {
		log.WithField("eventType", event.Type).Infof("%s event detected", resourceName)
		config, err := fetchHomerConfig(clientset, crdClient, traefikClient)
		if err != nil {
			log.WithError(err).Error("Error fetching Homer config")
			return
		}
		if err := writeToFile(config); err != nil {
			log.WithError(err).Error("Error writing config file")
		}
	}

	for _, ns := range watchedNamespaces {
		ns := ns
		runWatcherLoop(
			"Ingress("+ns+")",
			func() (watch.Interface, error) {
				return clientset.NetworkingV1().Ingresses(ns).Watch(context.TODO(), metav1.ListOptions{})
			},
			eventHandler,
			stopCh,
		)
	}

	for _, ns := range watchedNamespaces {
		ns := ns
		runWatcherLoop(
			"IngressRoute("+ns+")",
			func() (watch.Interface, error) {
				crdExists, err := checkCRDExists(crdClient, "ingressroutes.traefik.io")
				if err != nil || !crdExists {
					return nil, err
				}
				return traefikClient.TraefikV1alpha1().IngressRoutes(ns).Watch(context.TODO(), metav1.ListOptions{})
			},
			eventHandler,
			stopCh,
		)
	}
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

	go wait.Until(func() {
		config, err := fetchHomerConfig(clientset, crdClient, traefikClient)
		if err != nil {
			log.WithError(err).Error("Error during periodic refresh")
			return
		}
		if err := writeToFile(config); err != nil {
			log.WithError(err).Error("Error writing to file during periodic refresh")
		}
	}, 10*time.Minute, stopStructCh)

	go watchIngresses(clientset, crdClient, traefikClient, stopStructCh)

	<-stopStructCh
}
