package main

import (
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	defaultConfigFilePath     = "/www/assets/config.yml"
	defaultBaseConfigFilePath = "/www/assets/base_config.yml"
	configSeparator           = "\n# Automatically generated config:\n"
	ingressRouteCRDName       = "ingressroutes.traefik.io"

	homerServiceName  = "homer.service.name"
	homerServiceIcon  = "homer.service.icon"
	homerServiceRank  = "homer.service.rank"
	homerItemName     = "homer.item.name"
	homerItemLogo     = "homer.item.logo"
	homerItemURL      = "homer.item.url"
	homerItemType     = "homer.item.type"
	homerItemExcluded = "homer.item.excluded"
	homerItemRank     = "homer.item.rank"

	defaultServiceName = "default"
)

var ingressRouteGVR = schema.GroupVersionResource{
	Group: "traefik.io", Version: "v1alpha1", Resource: "ingressroutes",
}

type IngressRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              IngressRouteSpec `json:"spec"`
}

type IngressRouteSpec struct {
	Routes []IngressRouteRoute `json:"routes"`
}

type IngressRouteRoute struct {
	Match string `json:"match"`
}

type HomerItem struct {
	Name string `yaml:"name"`
	Logo string `yaml:"logo,omitempty"`
	URL  string `yaml:"url"`
	Type string `yaml:"type,omitempty"`
	Rank int    `yaml:"-"`
}

type HomerService struct {
	Name  string      `yaml:"name"`
	Icon  string      `yaml:"icon,omitempty"`
	Items []HomerItem `yaml:"items"`
	Rank  int         `yaml:"-"`
}

type HomerConfig struct {
	Services []HomerService `yaml:"services"`
}

type rankedName interface {
	GetRank() int
	GetName() string
}

func sortByRankAndName[T rankedName](entries []T) {
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		switch {
		case left.GetRank() != 0 && right.GetRank() != 0:
			if left.GetRank() != right.GetRank() {
				return left.GetRank() < right.GetRank()
			}
		case left.GetRank() != 0:
			return true
		case right.GetRank() != 0:
			return false
		}

		return strings.ToLower(left.GetName()) < strings.ToLower(right.GetName())
	})
}

func (item HomerItem) GetRank() int          { return item.Rank }
func (item HomerItem) GetName() string       { return item.Name }
func (service HomerService) GetRank() int    { return service.Rank }
func (service HomerService) GetName() string { return service.Name }
