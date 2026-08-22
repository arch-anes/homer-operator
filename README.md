# Homer Operator

[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/homer-operator)](https://artifacthub.io/packages/search?repo=homer-operator)

The **Homer Operator** watches Kubernetes Ingresses and Traefik IngressRoutes, then generates a [Homer](https://github.com/bastienwirtz/homer) configuration from their annotations.

---

## Installation

| **Method**       | **Command**                                                                                   |
|------------------|-----------------------------------------------------------------------------------------------|
| Add Repository   | `helm repo add homer-operator https://arch-anes.github.io/homer-operator`                     |
| Install          | `helm install homer-operator homer-operator/homer-operator`                                   |
| Install via OCI  | `helm install homer-operator oci://ghcr.io/arch-anes/homer-operator/homer-operator`           |

## Usage

The operator automatically detects **Ingresses** and adds them to Homer. Customize its behavior by adding annotations to your Kubernetes `Ingress` or Traefik `IngressRoute`.

### Watching namespaces

By default, the operator watches all namespaces. To limit the scope, set `watchedNamespaces` in the Helm values:

```yaml
watchedNamespaces:
  - default
  - internal-tools
```

When running the binary directly, use a comma-separated `WATCHED_NAMESPACES` environment variable. Whitespace and duplicate namespace names are ignored.

### Customizing Categories

| **Annotation**               | **Description**                                                                      |
|------------------------------|--------------------------------------------------------------------------------------|
| `homer.service.name`         | Group items under a specific category (e.g., `homer.service.name: 'some category'`). |
| `homer.service.icon`         | Set an icon for the category (e.g., `homer.service.icon: 'some icon'`).              |
| `homer.service.rank`         | Put ranked categories before unranked categories. Lower ranks appear first.          |

### Customizing Items

| **Annotation**               | **Description**                                                                   |
|------------------------------|-----------------------------------------------------------------------------------|
| `homer.item.excluded`        | Exclude an Ingress from appearing in Homer (e.g., `homer.item.excluded: 'true'`). |
| `homer.item.name`            | Rename an item (e.g., `homer.item.name: 'new name'`).                             |
| `homer.item.logo`            | Add a logo for the item (e.g., `homer.item.logo: 'path-to-logo'`).                |
| `homer.item.type`            | Define the type of the item (e.g., `homer.item.type: 'SomeType'`).                |
| `homer.item.url`             | Override the URL inferred from the resource host.                                 |
| `homer.item.rank`            | Put ranked items before unranked items. Lower ranks appear first.                 |

Items without an explicit rank are sorted alphabetically. Invalid boolean or numeric annotations are logged and treated as their default values.

## Development

The project requires the Go version declared in `go.mod`.

```sh
go test -race ./...
go vet ./...
docker build .
helm lint ./charts/homer-operator
helm template homer-operator ./charts/homer-operator
```

The runtime is intentionally separated into small units: `config.go` handles annotation parsing and file output, `resources.go` reads Kubernetes resources, `watcher.go` owns reconciliation and watch lifecycle, and `main.go` only wires the application together.
