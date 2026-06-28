package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"sync/atomic"
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

var (
	tracer trace.Tracer

	// --- RED Metrics (Rate, Errors, Duration) ---
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "order_service_http_requests_total",
		Help: "Total HTTP requests by method, path, and status code",
	}, []string{"method", "path", "status_code"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "order_service_http_request_duration_seconds",
		Help:    "HTTP request latency distribution",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.15, 0.2, 0.3, 0.5, 1.0},
	}, []string{"method", "path", "status_code"})

	httpActiveRequests = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "order_service_http_active_requests",
		Help: "Number of HTTP requests currently being processed",
	})

	// --- Business Metrics ---
	ordersProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "order_service_orders_processed_total",
		Help: "Total orders processed by product_id and status",
	}, []string{"product_id", "status"})

	orderRevenueTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "order_service_revenue_dollars_total",
		Help: "Total revenue in dollars by product_id",
	}, []string{"product_id"})

	orderQuantityTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "order_service_items_sold_total",
		Help: "Total items sold by product_id",
	}, []string{"product_id"})

	orderValueDistribution = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "order_service_order_value_dollars",
		Help:    "Distribution of individual order values in dollars",
		Buckets: []float64{5, 10, 25, 50, 100, 250, 500, 1000},
	})

	// --- Downstream Dependency Metrics ---
	inventoryCallDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "order_service_inventory_call_duration_seconds",
		Help:    "Latency of calls to inventory-service",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.5},
	})

	inventoryCallErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "order_service_inventory_call_errors_total",
		Help: "Total failed calls to the inventory service",
	})

	// --- Resource / Saturation Metrics ---
	requestPayloadBytes = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "order_service_request_payload_bytes",
		Help:    "Size of incoming request payloads",
		Buckets: []float64{64, 128, 256, 512, 1024, 4096},
	})
)

// peakHourMultiplier is read atomically by the traffic simulation
var peakHourMultiplier int64 = 1

// Product catalog with price ranges for realistic simulation
var productCatalog = map[string]float64{
	"PROD-LAPTOP":  999.99,
	"PROD-PHONE":   699.99,
	"PROD-TABLET":  449.99,
	"PROD-WATCH":   299.99,
	"PROD-EARBUDS": 149.99,
	"PROD-CHARGER": 29.99,
	"PROD-CASE":    19.99,
	"PROD-CABLE":   9.99,
}

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
			semconv.ServiceName("order-service"),
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

func orderHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		httpRequestsTotal.WithLabelValues(r.Method, "/order", "405").Inc()
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	span := trace.SpanFromContext(ctx)
	start := time.Now()
	httpActiveRequests.Inc()
	defer httpActiveRequests.Dec()

	statusCode := "201"
	orderStatus := "success"
	defer func() {
		elapsed := time.Since(start).Seconds()
		httpRequestsTotal.WithLabelValues(r.Method, "/order", statusCode).Inc()
		httpRequestDuration.WithLabelValues(r.Method, "/order", statusCode).Observe(elapsed)
	}()

	// Simulate variable processing time (more realistic distribution)
	baseLatency := rand.Intn(80) + 10  // 10-90ms base
	jitter := rand.Intn(40)            // 0-40ms jitter
	// Occasional slow requests (latency spike simulation)
	if rand.Intn(100) < 3 {
		baseLatency += rand.Intn(300) + 200 // +200-500ms spike
	}
	time.Sleep(time.Duration(baseLatency+jitter) * time.Millisecond)

	var req OrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		statusCode = "400"
		orderStatus = "bad_request"
		span.RecordError(err)
		requestPayloadBytes.Observe(0)
		ordersProcessed.WithLabelValues("unknown", orderStatus).Inc()
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Record payload size
	body, _ := json.Marshal(req)
	requestPayloadBytes.Observe(float64(len(body)))

	span.SetAttributes(attribute.String("product_id", req.ProductID))
	span.SetAttributes(attribute.Int("quantity", req.Quantity))

	log.Printf("Received order for ProductID: %s, Quantity: %d", req.ProductID, req.Quantity)

	// Simulate random failure (5% base error rate, scales with load)
	errorChance := 5 + int(atomic.LoadInt64(&peakHourMultiplier))
	if rand.Intn(100) < errorChance {
		statusCode = "500"
		orderStatus = "internal_error"
		err := errors.New("simulated internal server error")
		span.RecordError(err)
		log.Printf("ERROR: %v", err)
		ordersProcessed.WithLabelValues(req.ProductID, orderStatus).Inc()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Call Inventory Service
	inventoryURL := os.Getenv("INVENTORY_SERVICE_URL")
	if inventoryURL == "" {
		inventoryURL = "http://localhost:8081"
	}

	err := callInventoryService(ctx, inventoryURL, req)
	if err != nil {
		statusCode = "500"
		orderStatus = "inventory_error"
		span.RecordError(err)
		log.Printf("ERROR: Failed to call inventory service: %v", err)
		ordersProcessed.WithLabelValues(req.ProductID, orderStatus).Inc()
		inventoryCallErrors.Inc()
		http.Error(w, "Failed to process inventory", http.StatusInternalServerError)
		return
	}

	// Compute revenue
	price, ok := productCatalog[req.ProductID]
	if !ok {
		price = 49.99 // default price for unknown products
	}
	revenue := price * float64(req.Quantity)
	orderRevenueTotal.WithLabelValues(req.ProductID).Add(revenue)
	orderQuantityTotal.WithLabelValues(req.ProductID).Add(float64(req.Quantity))
	orderValueDistribution.Observe(revenue)
	ordersProcessed.WithLabelValues(req.ProductID, orderStatus).Inc()

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte("Order processed successfully\n"))
	log.Printf("Order processed successfully for ProductID: %s, Revenue: $%.2f", req.ProductID, revenue)
}

func callInventoryService(ctx context.Context, inventoryURL string, req OrderRequest) error {
	ctx, span := tracer.Start(ctx, "callInventoryService")
	defer span.End()

	start := time.Now()
	defer func() {
		inventoryCallDuration.Observe(time.Since(start).Seconds())
	}()

	body, _ := json.Marshal(req)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, inventoryURL+"/inventory/check", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Inject trace context
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(httpReq.Header))

	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("inventory service returned status: %d", resp.StatusCode)
	}

	return nil
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK\n"))
}

func main() {
	log.Println("Starting Order Service...")

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

	tracer = otel.Tracer("order-service")

	mux := http.NewServeMux()

	// OpenTelemetry HTTP instrumentation
	mux.Handle("/order", otelhttp.NewHandler(http.HandlerFunc(orderHandler), "OrderEndpoint"))
	mux.Handle("/health", http.HandlerFunc(healthHandler))

	// Prometheus Metrics Endpoint
	mux.Handle("/metrics", promhttp.Handler())

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
