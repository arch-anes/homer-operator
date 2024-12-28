# Homer Operator

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/homer-operator)](https://artifacthub.io/packages/search?repo=homer-operator)

The **Homer Operator** automatically generates and manages Homer configurations. 

---

## Installation

### Using Helm

| **Method**       | **Command**                                                                                   |
|------------------|-----------------------------------------------------------------------------------------------|
| Add Repository   | `helm repo add homer-operator https://arch-anes.github.io/homer-operator`                     |
| Install          | `helm install homer-operator homer-operator/homer-operator`                                   |
| Install via OCI  | `helm install homer-operator oci://ghcr.io/arch-anes/homer-operator/homer-operator`           |

---

## Usage

The operator automatically detects **Ingresses** and adds them to Homer. Customize its behavior by adding annotations to your Kubernetes `Ingress` or Traefik `IngressRoute`.

### Customizing Categories

| **Annotation**               | **Description**                                                                      |
|------------------------------|--------------------------------------------------------------------------------------|
| `homer.service.name`         | Group items under a specific category (e.g., `homer.service.name: 'some category'`). |
| `homer.service.icon`         | Set an icon for the category (e.g., `homer.service.icon: 'some icon'`).              |

### Customizing Items

| **Annotation**               | **Description**                                                                   |
|------------------------------|-----------------------------------------------------------------------------------|
| `homer.item.excluded`        | Exclude an Ingress from appearing in Homer (e.g., `homer.item.excluded: 'true'`). |
| `homer.item.name`            | Rename an item (e.g., `homer.item.name: 'new name'`).                             |
| `homer.item.logo`            | Add a logo for the item (e.g., `homer.item.logo: 'path-to-logo'`).                |
| `homer.item.type`            | Define the type of the item (e.g., `homer.item.type: 'SomeType'`).                |
| `homer.item.rank`            | Reorder items (e.g., `homer.item.rank: 'position'`).                              |
