# homer-operator

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/homer-operator)](https://artifacthub.io/packages/search?repo=homer-operator)

A homer operator that automatically generated homer config

# Installation

## Helm
```
helm install homer-operator oci://ghcr.io/arch-anes/homer-operator/homer-operator
```

# How to

The operator automatically picks up ingresses and add them to homer.

To customize the behavior, add any of the following annotations to the list of the ingress' annotations.

## Category

### Specify custom category (a.k.a service)
To add an item under a category, add `homer.service.name: 'some category'` annotation.

### Specify an icon for the category
To specify an icon for the category, add `homer.service.icon: 'some icon'` annotation.

## Item

### Exclude an ingress
To exclude an ingress, add `homer.item.excluded: 'true'` annotation.

### Rename an item
By default, ingresses' names are used to deduce the name of a service's item in Homer.
To override the behavior, add `homer.item.name: 'new name'`  annotation.

### Specify a logo
To specify a logo for the item, add `homer.item.logo: 'path-to-logo'` annotation.

### Specify a type
To specify a type for the item, add `homer.item.type: 'SomeType'` annotation.

### Reorder items
To reorder the items, add `homer.item.rank: 'position'` annotation.
