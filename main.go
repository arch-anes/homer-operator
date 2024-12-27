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
	"gopkg.in/yaml.v3"
	networkingv1 "k8s.io/api/networking/v1"
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

func deduceURL(ingress networkingv1.Ingress) string {
	if len(ingress.Spec.Rules) > 0 {
		return "https://" + ingress.Spec.Rules[0].Host
	}
	return ""
}

func extractHomerAnnotations(ingress networkingv1.Ingress) *HomerItem {
	annotations := ingress.Annotations
	item := &HomerItem{
		Name:     getAnnotationOrDefault(annotations, "homer.item.name", strcase.ToCamel(ingress.Name)),
		Logo:     annotations["homer.item.logo"],
		URL:      getAnnotationOrDefault(annotations, "homer.item.url", deduceURL(ingress)),
		Type:     annotations["homer.item.type"],
		Excluded: ignoreError(strconv.ParseBool(getAnnotationOrDefault(annotations, "homer.item.excluded", "false"))),
		Rank:     ignoreError(strconv.Atoi(getAnnotationOrDefault(annotations, "homer.item.rank", "0"))),
	}

	if item.Excluded {
		log.WithField("ingress", ingress.Name).Info("Skipping excluded ingress")
		return nil
	}

	if item.Name == "" || item.URL == "" {
		log.WithField("ingress", ingress.Name).Warn("Skipping invalid ingress")
		return nil
	}
	return item
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

func fetchAllIngresses(clientset *kubernetes.Clientset) ([]networkingv1.Ingress, error) {
	var allIngresses []networkingv1.Ingress
	continueToken := ""

	for {
		options := metav1.ListOptions{Continue: continueToken}
		ingressList, err := clientset.NetworkingV1().Ingresses("").List(context.TODO(), options)
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

func fetchHomerConfig(clientset *kubernetes.Clientset) (HomerConfig, error) {
	ingresses, err := fetchAllIngresses(clientset)
	if err != nil {
		return HomerConfig{}, err
	}

	serviceMap := make(map[string]*HomerService)

	for _, ingress := range ingresses {
		log.WithField("ingress", ingress.Name).Debug("Processing ingress")
		item := extractHomerAnnotations(ingress)
		if item == nil {
			continue
		}

		annotations := ingress.Annotations
		service := &HomerService{
			Name:  getAnnotationOrDefault(annotations, "homer.service.name", "default"),
			Icon:  annotations["homer.service.icon"],
			Items: []HomerItem{*item},
			Rank:  ignoreError(strconv.Atoi(getAnnotationOrDefault(annotations, "homer.service.rank", "0"))),
		}

		if existingService, exists := serviceMap[service.Name]; exists {
			existingService.Items = append(existingService.Items, *item)
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

	return HomerConfig{Services: services}, nil
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

func watchIngresses(clientset *kubernetes.Clientset) {
	watcher, err := clientset.NetworkingV1().Ingresses("").Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.WithError(err).Error("Failed to start watching ingresses")
		return
	}
	defer watcher.Stop()

	log.Info("Watching for ingress changes...")

	for event := range watcher.ResultChan() {
		if event.Type == watch.Added || event.Type == watch.Modified || event.Type == watch.Deleted {
			log.WithField("eventType", event.Type).Info("Ingress event detected")
			config, err := fetchHomerConfig(clientset)
			if err != nil {
				log.WithError(err).Error("Error fetching Homer config")
				continue
			}
			if err := writeToFile(config); err != nil {
				log.WithError(err).Error("Error writing to file")
			}
		} else {
			log.WithField("eventType", event.Type).Warn("Unhandled event type")
		}
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

	go wait.Until(func() {
		config, err := fetchHomerConfig(clientset)
		if err != nil {
			log.WithError(err).Error("Error during periodic refresh")
			return
		}
		if err := writeToFile(config); err != nil {
			log.WithError(err).Error("Error writing to file during periodic refresh")
		}
	}, 10*time.Minute, stopStructCh)

	go watchIngresses(clientset)

	<-stopStructCh
}
