# AKS Deployment Guide — College Report Setup (Ubuntu/Bash)

This guide walks you through deploying the complete SRE Microservices project on Azure Kubernetes Service (AKS) and capturing Grafana dashboard screenshots for your college report.

## Prerequisites

```bash
# Install Azure CLI
curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash

# Install kubectl
sudo az aks install-cli

# Install Docker
sudo apt-get update
sudo apt-get install -y docker.io
sudo systemctl enable docker
sudo systemctl start docker
sudo usermod -aG docker $USER
# Log out and log back in for docker group to take effect
```

Then log in to Azure:
```bash
az login
```

---

## Phase 1: Create Azure Infrastructure (~5 min)

```bash
# Pick a unique name for your container registry (lowercase, no hyphens)
ACR_NAME="sreprojectacr"

# Create Resource Group
az group create --name sre-rg --location eastus

# Create Azure Container Registry
az acr create --resource-group sre-rg --name $ACR_NAME --sku Basic

# Create AKS Cluster
az aks create \
    --resource-group sre-rg \
    --name sre-aks-cluster \
    --node-count 2 \
    --generate-ssh-keys \
    --attach-acr $ACR_NAME

# Connect kubectl to your cluster
az aks get-credentials --resource-group sre-rg --name sre-aks-cluster

# Verify connection
kubectl get nodes
```

> [!IMPORTANT]
> You should see 2 nodes in `Ready` state. If not, wait a minute and retry `kubectl get nodes`.

---

## Phase 2: Build & Push Docker Images (~10 min)

```bash
# Login to your container registry
az acr login --name $ACR_NAME

# Build and push all 3 services
docker build -t $ACR_NAME.azurecr.io/order-service:latest ./order-service
docker push $ACR_NAME.azurecr.io/order-service:latest

docker build -t $ACR_NAME.azurecr.io/inventory-service:latest ./inventory-service
docker push $ACR_NAME.azurecr.io/inventory-service:latest

docker build -t $ACR_NAME.azurecr.io/traffic-generator:latest ./traffic-generator
docker push $ACR_NAME.azurecr.io/traffic-generator:latest
```

---

## Phase 3: Update K8s Manifests

Replace `<YOUR_ACR_NAME>` with your actual ACR name in all manifest files:

```bash
sed -i "s/<YOUR_ACR_NAME>/$ACR_NAME/g" \
    k8s/order-service.yaml \
    k8s/inventory-service.yaml \
    k8s/traffic-generator.yaml
```

Verify the replacement worked:
```bash
grep "image:" k8s/order-service.yaml k8s/inventory-service.yaml k8s/traffic-generator.yaml
```

---

## Phase 4: Deploy to AKS (~3 min)

Run these commands **in order**:

```bash
# Step 1: Create the namespace
kubectl apply -f k8s/00-namespace.yaml

# Step 2: Deploy the observability stack
kubectl apply -f k8s/10-otel-collector.yaml
kubectl apply -f k8s/11-prometheus.yaml
kubectl apply -f k8s/12-jaeger.yaml
kubectl apply -f k8s/13-loki.yaml

# Step 3: Create the dashboard ConfigMap from the JSON file
kubectl create configmap grafana-dashboard-json \
    --from-file=sre-dashboard.json=infra/dashboards/sre-dashboard.json \
    -n sre-project

# Step 4: Deploy Grafana
kubectl apply -f k8s/14-grafana.yaml

# Step 5: Deploy the application services
kubectl apply -f k8s/order-service.yaml
kubectl apply -f k8s/inventory-service.yaml
kubectl apply -f k8s/traffic-generator.yaml
```

---

## Phase 5: Verify Everything is Running

```bash
# Check all pods are Running
kubectl get pods -n sre-project
```

Expected output (all pods should show `Running`):
```
NAME                                READY   STATUS    RESTARTS   AGE
grafana-xxx                         1/1     Running   0          2m
inventory-service-xxx               1/1     Running   0          1m
inventory-service-yyy               1/1     Running   0          1m
jaeger-xxx                          1/1     Running   0          2m
loki-xxx                            1/1     Running   0          2m
otel-collector-xxx                  1/1     Running   0          2m
order-service-xxx                   1/1     Running   0          1m
order-service-yyy                   1/1     Running   0          1m
prometheus-xxx                      1/1     Running   0          2m
traffic-generator-xxx               1/1     Running   0          1m
```

> [!TIP]
> If any pod shows `ImagePullBackOff`, double-check that you replaced `<YOUR_ACR_NAME>` correctly in Phase 3, and that the images were pushed successfully in Phase 2.

---

## Phase 6: Access Grafana Dashboard

### Option A: Get the External IP (LoadBalancer)

```bash
kubectl get svc grafana -n sre-project
```

Wait until `EXTERNAL-IP` shows an actual IP address (may take 1-2 minutes), then open:
```
http://<EXTERNAL-IP>:3000
```

### Option B: Port Forward (if LoadBalancer takes too long)

```bash
kubectl port-forward svc/grafana 3000:3000 -n sre-project
```
Then open: `http://localhost:3000`

### Login
- **Username**: `admin`
- **Password**: `admin`
- Skip the password change prompt

### Navigate to the Dashboard
1. Click the **hamburger menu** (☰) on the top-left
2. Click **Dashboards**
3. Click **SRE Microservices Dashboard**

---

## Phase 7: Wait for Data to Populate

> [!IMPORTANT]
> **Wait 2-3 minutes** after deployment for data to appear. The traffic generator needs time to start sending requests, and Prometheus needs to scrape at least 2-3 cycles (10s each) before `rate()` queries produce results.

### Verify traffic is flowing:
```bash
# Check traffic generator logs
kubectl logs -f deployment/traffic-generator -n sre-project

# You should see output like:
# Order sent: PROD-CABLE x3 -> 201
# Order sent: PROD-EARBUDS x1 -> 201
# Order sent: PROD-CHARGER x2 -> 500
```

### Verify Prometheus is collecting:
```bash
kubectl port-forward svc/prometheus 9090:9090 -n sre-project &
```
Open `http://localhost:9090`, go to **Graph**, and type:
```
order_service_http_requests_total
```
If you see results, everything is working!

---

## Phase 8: Capture Screenshots for Your Report

Once data has been flowing for 3-5 minutes, your dashboard will have dense, rich graphs.

### Recommended Screenshots

| Screenshot | What it shows | Dashboard Section |
|---|---|---|
| **Full Overview Row** | Request rate, error %, p99, revenue, items sold | Top stat panels |
| **RED Metrics** | Rate by status code, latency distribution, error rate with SLO line | Second row |
| **Business Metrics** | Orders by product (stacked), revenue per product, order value | Third row |
| **Inventory Gauges** | Stock levels fluctuating, out-of-stock events, lookup latency | Fourth row |
| **Dependencies** | Downstream inventory call latency, error rate, payload sizes | Fifth row |
| **System Metrics** | Goroutines, heap memory, GC pauses, file descriptors | Sixth row |
| **Jaeger Traces** | End-to-end trace from order-service → inventory-service | Jaeger UI |

### Tips for Clean Report Screenshots
1. Set the time range to **Last 5 minutes** (top-right in Grafana)
2. Wait until all panels show data (no "No data" messages)
3. Use **Kiosk mode** — append `?kiosk` to the URL for clean screenshots without sidebars
4. Use `gnome-screenshot -a` or `Shift+PrtSc` on Ubuntu to take region screenshots

### Access Jaeger for Trace Screenshots
```bash
kubectl port-forward svc/jaeger 16686:16686 -n sre-project &
```
Then open `http://localhost:16686`, select **order-service** from the Service dropdown, and click **Find Traces**.

---

## Quick Reference: Useful kubectl Commands

```bash
# View all resources in the namespace
kubectl get all -n sre-project

# Describe a failing pod for debugging
kubectl describe pod <pod-name> -n sre-project

# View logs for a specific service
kubectl logs deployment/order-service -n sre-project
kubectl logs deployment/inventory-service -n sre-project
kubectl logs deployment/traffic-generator -n sre-project

# Restart a deployment
kubectl rollout restart deployment/order-service -n sre-project

# Scale traffic generator up for more data
kubectl scale deployment/traffic-generator --replicas=3 -n sre-project

# Check resource usage
kubectl top pods -n sre-project
```

---

## Troubleshooting

| Issue | Solution |
|---|---|
| Pods in `ImagePullBackOff` | Check ACR name in YAML files; run `az acr login --name $ACR_NAME` |
| "No data" on dashboard | Wait 2-3 min; check traffic-generator logs; verify Prometheus targets |
| Grafana login stuck | Clear browser cache; try incognito window |
| External IP is `<pending>` | Use `kubectl port-forward` as Option B |
| Prometheus shows no targets | Check that service names match in `prometheus-config` ConfigMap |
| `az aks create` fails | Check your Azure subscription has enough quota for 2 nodes |

---

## Cleanup (After Report)

```bash
# Delete the namespace (removes everything in the cluster)
kubectl delete namespace sre-project

# Delete all Azure resources (AKS cluster, ACR, resource group)
az group delete --name sre-rg --yes --no-wait
```

> [!CAUTION]
> Always clean up after your report to avoid Azure charges!
