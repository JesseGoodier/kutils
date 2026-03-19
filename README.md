# kutils <!-- omit from toc -->

Kubernetes CLI utilities.

- [Installation](#installation)
  - [Homebrew (macOS and Linux)](#homebrew-macos-and-linux)
  - [go install](#go-install)
  - [Build from source](#build-from-source)
- [kgc](#kgc)
  - [Features](#features)
  - [Usage](#usage)
- [kgpv](#kgpv)
  - [Features](#features-1)
  - [Usage](#usage-1)
- [Requirements](#requirements)
- [License](#license)


## Tools <!-- omit from toc -->

- **[kgc](#kgc)** — `kubectl get pods` with color-coded status and last restart reason
- **[kgpv](#kgpv)** — List PersistentVolumes with bound PVCs, pods, zones, storageClass and sizes

## Installation

### Homebrew (macOS and Linux)

```sh
brew tap jessegoodier/kutils
brew install jessegoodier/kutils/kgc
brew install jessegoodier/kutils/kgpv
```

### go install

```sh
go install github.com/jessegoodier/kutils/cmd/kgc@latest
go install github.com/jessegoodier/kutils/cmd/kgpv@latest
```

### Build from source

```sh
git clone https://github.com/jessegoodier/kutils.git
cd kutils
go build -o kgc ./cmd/kgc
go build -o kgpv ./cmd/kgpv
```

---

## kgc

Like `kubectl get pods`, but focused on containers and highlighting the last restart reason.

```
kgc -A
NAMESPACE    NAME                                        READY  STATUS     RESTARTS  AGE    LAST RESTART REASON
monitoring   prometheus-kube-prometheus-stack-0          2/2    Running    0         2d23h
opencost     opencost-9fcf95fc8-6tns1                    2/2    Running    3         2d23h  opencost: Error
```

### Features

- Color-coded status: green (healthy), yellow (pending/not-ready), red (failed)
- Watch mode for real-time updates (`-w`)
- All-namespaces view (`-A`)
- Filter to only pods with restarts (`-r`)
- Shows last restart reason per container
- Adapts column widths dynamically

### Usage

```
kgc [flags]

Flags:
  -n, --namespace <namespace>  Namespace to list pods in.  (default: current context)
  -A                           List pods across all namespaces.
  -w, --watch                  Watch for pod changes.
  -r, --has-restarts           Only show pods with restarts.
  --show-image                 Show container image column.
  --kubeconfig <path>          Path to kubeconfig file.
  -v, --version                Print version and exit.
  -h, --help                   Show this help message.
```

---

## kgpv

List PersistentVolumes with their bound PVCs, using pods, availability zones, and sizes.

```
kgpv
NAMESPACE    POD                    ZONE          SIZE  PVC
production   api-server-abc123      us-east-1a    10Gi  api-data
production   worker-xyz789          us-east-1b    50Gi  worker-storage
```

### Features

- Shows PV → PVC → Pod relationship at a glance
- Extracts zone from node affinity or labels
- Converts storage sizes to Gi format
- Color-coded output per column type
- Concurrent PV processing for speed
- Dynamic column width adaptation

### Usage

```
kgpv [flags]

Flags:
  --hide-pod             Hide the pod column.
  --show-pv-name         Show the PV name column.
  --show-reclaim-policy  Show the reclaim policy column.
  --show-all-fields           Show all columns.
  --sort <column>        Sort by column: namespace, pod, zone, size, pvc, pv.  (default: namespace)
  --output <format>      Output format: csv, json, yaml.
  -v, --version          Print version and exit.
```

---

## Requirements

- A valid kubeconfig with cluster access

## License

MIT
