package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Simulated stock levels per product (realistic inventory state)
var stockLevels = map[string]int{
	"PROD-LAPTOP":  50,
	"PROD-PHONE":   120,
	"PROD-TABLET":  80,
	"PROD-WATCH":   200,
	"PROD-EARBUDS": 500,
	"PROD-CHARGER": 1000,
	"PROD-CASE":    800,
	"PROD-CABLE":   1500,
}

var (
	tracer trace.Tracer

	// --- RED Metrics ---
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "inventory_service_http_requests_total",
		Help: "Total HTTP requests by method, path, and status code",
	}, []string{"method", "path", "status_code"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "inventory_service_http_request_duration_seconds",
		Help:    "HTTP request latency distribution",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.15, 0.2, 0.3, 0.5},
	}, []string{"method", "path", "status_code"})

	httpActiveRequests = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "inventory_service_http_active_requests",
		Help: "Number of HTTP requests currently being processed",
	})

	// --- Business Metrics ---
	inventoryChecksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "inventory_service_checks_total",
		Help: "Total inventory checks by product and result",
	}, []string{"product_id", "result"})

	inventoryStockLevel = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "inventory_service_stock_level",
		Help: "Current stock level per product",
	}, []string{"product_id"})

	inventoryStockEmpty = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "inventory_service_stock_empty_total",
		Help: "Total out-of-stock events by product",
	}, []string{"product_id"})

	inventoryLookupDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "inventory_service_lookup_duration_seconds",
		Help:    "Time spent looking up inventory per product",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1},
	}, []string{"product_id"})
)

func initTracer() (*sdktrace.TracerProvider, error) {
	ctx := context.Background()

	otelAgentAddr := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otelAgentAddr == "" {
		otelAgentAddr = "localhost:4317"
	}

	conn, err := grpc.DialContext(ctx, otelAgentAddr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(), grpc.WithTimeout(2*time.Second))
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("inventory-service"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	bsp := sdktrace.NewBatchSpanProcessor(traceExporter)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)

	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	return tracerProvider, nil
}

type OrderRequest struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

func inventoryCheckHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpRequestsTotal.WithLabelValues(r.Method, "/inventory/check", "405").Inc()
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	span := trace.SpanFromContext(ctx)
	start := time.Now()
	httpActiveRequests.Inc()
	defer httpActiveRequests.Dec()

	statusCode := "200"
	defer func() {
		elapsed := time.Since(start).Seconds()
		httpRequestsTotal.WithLabelValues(r.Method, "/inventory/check", statusCode).Inc()
		httpRequestDuration.WithLabelValues(r.Method, "/inventory/check", statusCode).Observe(elapsed)
	}()

	// Simulate DB lookup latency
	lookupStart := time.Now()
	time.Sleep(time.Duration(rand.Intn(40)+5) * time.Millisecond)

	var req OrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		statusCode = "400"
		span.RecordError(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	inventoryLookupDuration.WithLabelValues(req.ProductID).Observe(time.Since(lookupStart).Seconds())
	span.SetAttributes(attribute.String("product_id", req.ProductID))

	log.Printf("Checking inventory for ProductID: %s, Quantity: %d", req.ProductID, req.Quantity)

	// Update stock level gauge (simulate fluctuating stock)
	currentStock, exists := stockLevels[req.ProductID]
	if !exists {
		currentStock = rand.Intn(100) + 10
	}
	// Simulate stock fluctuation
	currentStock += rand.Intn(10) - 5
	if currentStock < 0 {
		currentStock = 0
	}
	stockLevels[req.ProductID] = currentStock
	inventoryStockLevel.WithLabelValues(req.ProductID).Set(float64(currentStock))

	// Out of stock check (varies by product)
	outOfStockChance := 2
	if currentStock < 20 {
		outOfStockChance = 15  // higher chance if low stock
	}
	if rand.Intn(100) < outOfStockChance {
		statusCode = "409"
		inventoryStockEmpty.WithLabelValues(req.ProductID).Inc()
		inventoryChecksTotal.WithLabelValues(req.ProductID, "out_of_stock").Inc()
		err := fmt.Errorf("product %s is out of stock", req.ProductID)
		span.RecordError(err)
		log.Printf("ERROR: %v", err)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	inventoryChecksTotal.WithLabelValues(req.ProductID, "available").Inc()
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Inventory available\n"))
	log.Printf("Inventory check successful for ProductID: %s (stock: %d)", req.ProductID, currentStock)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK\n"))
}

func main() {
	log.Println("Starting Inventory Service...")

	tp, err := initTracer()
	if err != nil {
		log.Printf("Failed to initialize tracer: %v, continuing without tracing", err)
	} else {
		defer func() {
			if err := tp.Shutdown(context.Background()); err != nil {
				log.Printf("Error shutting down tracer provider: %v", err)
			}
		}()
	}

	tracer = otel.Tracer("inventory-service")

	// Initialize stock level gauges
	for product, level := range stockLevels {
		inventoryStockLevel.WithLabelValues(product).Set(float64(level))
	}

	mux := http.NewServeMux()

	// OpenTelemetry HTTP instrumentation
	mux.Handle("/inventory/check", otelhttp.NewHandler(http.HandlerFunc(inventoryCheckHandler), "InventoryCheckEndpoint"))
	mux.Handle("/health", http.HandlerFunc(healthHandler))

	// Prometheus Metrics Endpoint
	mux.Handle("/metrics", promhttp.Handler())

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
