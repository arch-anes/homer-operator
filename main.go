package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

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
	configSeparator = "\n#Automatically generated config:\n"
)

type HomerItem struct {
	Name string `yaml:"name"`
	Logo string `yaml:"logo"`
	URL  string `yaml:"url"`
	Type string `yaml:"type"`
}

type HomerService struct {
	Name  string       `yaml:"name"`
	Icon  string       `yaml:"icon"`
	Items []HomerItem  `yaml:"items"`
}

type HomerConfig struct {
	Services []HomerService `yaml:"services"`
}

func isItemExcluded(ingress networkingv1.Ingress) bool {
	annotations := ingress.Annotations
	excluded, err := strconv.ParseBool(getAnnotationOrDefault(annotations, "homer.item.excluded", "false"))
	if err != nil {
		fmt.Printf("Error parsing 'homer.item.excluded' from ingress: %s\n", ingress.Name)
		return false
	}
	return excluded
}

func extractHomerAnnotations(ingress networkingv1.Ingress) *HomerItem {
	if isItemExcluded(ingress) {
		fmt.Printf("Skipping excluded ingress: %s\n", ingress.Name)
		return nil
	}

	annotations := ingress.Annotations
	item := &HomerItem{
		Name: getAnnotationOrDefault(annotations, "homer.item.name", ingress.Name),
		Logo: annotations["homer.item.logo"],
		URL:  getAnnotationOrDefault(annotations, "homer.item.url", deduceURL(ingress)),
		Type: annotations["homer.item.type"],
	}

	if item.Name == "" || item.URL == "" {
		return nil
	}
	return item
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

func fetchHomerConfig(clientset *kubernetes.Clientset) (HomerConfig, error) {
	ingressList, err := clientset.NetworkingV1().Ingresses("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return HomerConfig{}, fmt.Errorf("failed to list ingresses: %w", err)
	}

	serviceMap := make(map[string]*HomerService)

	for _, ingress := range ingressList.Items {
		item := extractHomerAnnotations(ingress)
		if item == nil {
			continue
		}

		serviceName := getAnnotationOrDefault(ingress.Annotations, "homer.service.name", "default")

		if existingService, exists := serviceMap[serviceName]; exists {
			existingService.Items = append(existingService.Items, *item)
		} else {
			serviceMap[serviceName] = &HomerService{
				Name:  serviceName,
				Icon:  ingress.Annotations["homer.service.icon"],
				Items: []HomerItem{*item},
			}
		}
	}

	var services []HomerService
	for _, service := range serviceMap {
		services = append(services, *service)
	}

	return HomerConfig{Services: services}, nil
}

func mergeWithBaseConfig(generatedConfig []byte) ([]byte, error) {
	baseConfig, err := os.ReadFile(baseConfigFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			baseConfig = []byte("")
		} else {
			return nil, fmt.Errorf("failed to read base config: %w", err)
		}
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
		return fmt.Errorf("failed to merge configs: %w", err)
	}

	err = os.WriteFile(configFilePath, finalConfig, 0644)
	if err != nil {
		return fmt.Errorf("failed to write YAML file: %w", err)
	}
	fmt.Printf("YAML file updated: %s\n", configFilePath)
	return nil
}

func watchIngresses(clientset *kubernetes.Clientset) {
	watcher, err := clientset.NetworkingV1().Ingresses("").Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		fmt.Printf("Failed to start watching ingresses: %v\n", err)
		return
	}
	defer watcher.Stop()

	fmt.Println("Watching for ingress changes...")

	for event := range watcher.ResultChan() {
		switch event.Type {
		case watch.Added, watch.Modified, watch.Deleted:
			fmt.Printf("Ingress event detected: %s\n", event.Type)
			config, err := fetchHomerConfig(clientset)
			if err != nil {
				fmt.Printf("Error fetching Homer config: %v\n", err)
				continue
			}
			err = writeToFile(config)
			if err != nil {
				fmt.Printf("Error writing to file: %v\n", err)
			}
		default:
			fmt.Printf("Unhandled event type: %s\n", event.Type)
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
		fmt.Println("Shutting down...")
	}()

	config, err := rest.InClusterConfig()
	if err != nil {
		fmt.Printf("Error loading in-cluster config: %v\n", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("Error creating Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	go wait.Until(func() {
		config, err := fetchHomerConfig(clientset)
		if err != nil {
			fmt.Printf("Error during periodic refresh: %v\n", err)
			return
		}
		err = writeToFile(config)
		if err != nil {
			fmt.Printf("Error writing to file during periodic refresh: %v\n", err)
		}
	}, 10*time.Minute, stopStructCh)

	go watchIngresses(clientset)

	<-stopStructCh
}
