# Microservices Site Reliability Engineering (SRE) Project

This project demonstrates a production-grade Site Reliability Engineering (SRE) implementation for microservices using Go, OpenTelemetry, and the Prometheus/Loki/Jaeger/Grafana stack. It is deployed on **Azure Kubernetes Service (AKS)**.

## Architecture

The project consists of three microservices and a full observability stack:

| Component | Description |
|---|---|
| **`order-service`** | API that accepts customer orders. Tracks per-product revenue, latency histograms, error rates, and downstream dependency health. |
| **`inventory-service`** | Called by `order-service` to check stock. Maintains real-time stock level gauges, per-product lookup latency, and out-of-stock counters. |
| **`traffic-generator`** | Go-based load generator that simulates realistic e-commerce traffic with weighted product selection, burst patterns, and occasional traffic spikes. |

### Product Catalog (8 Products)
| Product ID | Price | Traffic Weight |
|---|---|---|
| PROD-LAPTOP | $999.99 | Low |
| PROD-PHONE | $699.99 | Low |
| PROD-TABLET | $449.99 | Medium |
| PROD-WATCH | $299.99 | Medium |
| PROD-EARBUDS | $149.99 | High |
| PROD-CHARGER | $29.99 | High |
| PROD-CASE | $19.99 | High |
| PROD-CABLE | $9.99 | Very High |

### Observability Stack
- **OpenTelemetry (OTel)**: Go SDK generates Traces and Metrics → OTel Collector.
- **OTel Collector**: Receives telemetry, exports to Prometheus (metrics) and Jaeger (traces).
- **Prometheus**: Scrapes and stores time-series metrics from all services.
- **Loki & Promtail**: Log aggregation from container logs.
- **Jaeger**: Distributed tracing for end-to-end request visualization.
- **Grafana**: Unified dashboard with **22 pre-configured panels** across 6 categories.

---

## SRE Concepts Implemented

### 1. Service Level Indicators (SLIs)
- **Availability (Error Rate)**: `order_service_http_requests_total{status_code=~"5.."}` / total requests
- **Latency**: p50, p90, p99 from `order_service_http_request_duration_seconds`
- **Throughput**: `rate(order_service_http_requests_total[1m])`

### 2. Service Level Objectives (SLOs)
- **Latency SLO**: 99% of `/order` requests complete under 200ms (rolling 30-day window)
- **Availability SLO**: 99.0% of `/order` requests return non-5xx status (rolling 30-day window)

### 3. Service Level Agreements (SLAs)
- **Example**: "If Availability SLO drops below 99.0% for a given month, customers receive a 10% credit."

---

## Grafana Dashboard Panels (22 Total)

| Row | Panel | Metric |
|---|---|---|
| **Overview** | Request Rate | `sum(rate(order_service_http_requests_total[1m]))` |
| | Error Rate % | 5xx / total * 100 |
| | p99 Latency | `histogram_quantile(0.99, ...)` |
| | Active Requests | `order_service_http_active_requests` |
| | Total Revenue | `sum(order_service_revenue_dollars_total)` |
| | Total Items Sold | `sum(order_service_items_sold_total)` |
| **RED Metrics** | Request Rate by Status Code | Grouped by 201, 400, 500 |
| | Latency Distribution | p50 / p90 / p99 time-series |
| | Error Rate % with SLO line | Error % vs 1% threshold |
| **Business** | Orders by Product (Stacked) | Per-product order rate |
| | Revenue Rate by Product | $/min per product |
| | Order Value Distribution | Avg and p90 order value |
| **Inventory** | Stock Levels by Product | Real-time gauges |
| | Out-of-Stock Events | Per-product bar chart |
| | Inventory Lookup Latency | p99 per product |
| **Dependencies** | Inventory Call Latency | p50/p99 downstream call |
| | Inventory Call Error Rate | Dependency failures |
| | Request Payload Size | p50/p99 bytes |
| **System** | Goroutines | Go runtime threads |
| | Heap Memory | `go_memstats_alloc_bytes` |
| | GC Pause Duration | Garbage collection |
| | Open File Descriptors | Process FDs |

---

## AKS Deployment (Step-by-Step)

### Prerequisites
- Azure CLI (`az`) installed and authenticated
- `kubectl` installed
- Docker Desktop installed
- An active Azure subscription

### Step 1: Create Azure Resources

```bash
# Set your ACR name (must be globally unique, lowercase, no hyphens)
export ACR_NAME=sreprojectacr

# Create Resource Group
az group create --name sre-rg --location eastus

# Create Azure Container Registry
az acr create --resource-group sre-rg --name $ACR_NAME --sku Basic

# Create AKS Cluster (2 nodes)
az aks create \
    --resource-group sre-rg \
    --name sre-aks-cluster \
    --node-count 2 \
    --generate-ssh-keys \
    --attach-acr $ACR_NAME

# Get Kubeconfig
az aks get-credentials --resource-group sre-rg --name sre-aks-cluster
```

### Step 2: Build and Push Docker Images

```bash
# Login to ACR
az acr login --name $ACR_NAME

# Build and Push all 3 images
docker build -t $ACR_NAME.azurecr.io/order-service:latest ./order-service
docker push $ACR_NAME.azurecr.io/order-service:latest

docker build -t $ACR_NAME.azurecr.io/inventory-service:latest ./inventory-service
docker push $ACR_NAME.azurecr.io/inventory-service:latest

docker build -t $ACR_NAME.azurecr.io/traffic-generator:latest ./traffic-generator
docker push $ACR_NAME.azurecr.io/traffic-generator:latest
```

### Step 3: Update K8s Manifests

Replace `<YOUR_ACR_NAME>` in these files with your actual ACR name:
```bash
# Linux/Mac
sed -i "s/<YOUR_ACR_NAME>/$ACR_NAME/g" k8s/order-service.yaml k8s/inventory-service.yaml k8s/traffic-generator.yaml

# Windows PowerShell
(Get-Content k8s/order-service.yaml) -replace '<YOUR_ACR_NAME>', $env:ACR_NAME | Set-Content k8s/order-service.yaml
(Get-Content k8s/inventory-service.yaml) -replace '<YOUR_ACR_NAME>', $env:ACR_NAME | Set-Content k8s/inventory-service.yaml
(Get-Content k8s/traffic-generator.yaml) -replace '<YOUR_ACR_NAME>', $env:ACR_NAME | Set-Content k8s/traffic-generator.yaml
```

### Step 4: Deploy Everything to AKS

```bash
# 1. Create namespace
kubectl apply -f k8s/00-namespace.yaml

# 2. Deploy observability stack
kubectl apply -f k8s/10-otel-collector.yaml
kubectl apply -f k8s/11-prometheus.yaml
kubectl apply -f k8s/12-jaeger.yaml
kubectl apply -f k8s/13-loki.yaml

# 3. Create the Grafana dashboard ConfigMap from the JSON file
kubectl create configmap grafana-dashboard-json \
    --from-file=sre-dashboard.json=infra/dashboards/sre-dashboard.json \
    -n sre-project

# 4. Deploy Grafana
kubectl apply -f k8s/14-grafana.yaml

# 5. Deploy applications
kubectl apply -f k8s/order-service.yaml
kubectl apply -f k8s/inventory-service.yaml
kubectl apply -f k8s/traffic-generator.yaml
```

### Step 5: Access the Dashboard

```bash
# Wait for all pods to be running
kubectl get pods -n sre-project -w

# Get the Grafana external IP
kubectl get svc grafana -n sre-project

# If the EXTERNAL-IP is <pending>, wait a minute and try again
```

Once you have the external IP, open `http://<EXTERNAL-IP>:3000` in your browser.
- **Username**: `admin`
- **Password**: `admin`
- Navigate to **Dashboards** → **SRE Microservices Dashboard**

### Step 6: Verify Data is Flowing

```bash
# Check traffic generator is sending requests
kubectl logs -f deployment/traffic-generator -n sre-project

# Check order-service is receiving requests
kubectl logs -f deployment/order-service -n sre-project

# Check Prometheus is scraping metrics
kubectl port-forward svc/prometheus 9090:9090 -n sre-project
# Then open http://localhost:9090 and query: order_service_http_requests_total
```

---

## Local Development (Docker Compose)

```bash
# Start the full stack locally
docker compose up -d --build

# Access dashboards
# Grafana: http://localhost:3000 (admin/admin)
# Prometheus: http://localhost:9090
# Jaeger: http://localhost:16686
```

---

## Project Structure

```
sre-project/
├── order-service/          # Go microservice (orders API)
│   ├── main.go
│   ├── go.mod / go.sum
│   └── Dockerfile
├── inventory-service/      # Go microservice (inventory checks)
│   ├── main.go
│   ├── go.mod / go.sum
│   └── Dockerfile
├── traffic-generator/      # Go load generator
│   ├── main.go
│   ├── go.mod
│   └── Dockerfile
├── infra/                  # Configuration files
│   ├── otel-collector-config.yml
│   ├── prometheus.yml
│   ├── promtail-config.yml
│   ├── grafana-datasource.yml
│   ├── grafana-dashboards.yml
│   └── dashboards/
│       └── sre-dashboard.json
├── k8s/                    # Kubernetes manifests
│   ├── 00-namespace.yaml
│   ├── 10-otel-collector.yaml
│   ├── 11-prometheus.yaml
│   ├── 12-jaeger.yaml
│   ├── 13-loki.yaml
│   ├── 14-grafana.yaml
│   ├── order-service.yaml
│   ├── inventory-service.yaml
│   └── traffic-generator.yaml
├── docker-compose.yml
└── README.md
```

## Cleanup

```bash
# Delete all AKS resources
kubectl delete namespace sre-project

# Delete Azure resources
az group delete --name sre-rg --yes --no-wait
```
