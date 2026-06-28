package main

import (
	"bytes"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"time"
)

// Product catalog matching the order-service products
var products = []struct {
	ID     string
	Weight int // relative traffic weight
}{
	{"PROD-CABLE", 25},    // cheap items bought most often
	{"PROD-CHARGER", 20},
	{"PROD-CASE", 18},
	{"PROD-EARBUDS", 15},
	{"PROD-WATCH", 10},
	{"PROD-TABLET", 6},
	{"PROD-PHONE", 4},
	{"PROD-LAPTOP", 2},   // expensive items bought least
}

func pickProduct() string {
	totalWeight := 0
	for _, p := range products {
		totalWeight += p.Weight
	}
	r := rand.Intn(totalWeight)
	for _, p := range products {
		r -= p.Weight
		if r < 0 {
			return p.ID
		}
	}
	return products[0].ID
}

func sendOrder(client *http.Client, baseURL string) {
	productID := pickProduct()
	quantity := rand.Intn(5) + 1 // 1-5 items per order

	body := fmt.Sprintf(`{"product_id": "%s", "quantity": %d}`, productID, quantity)

	resp, err := client.Post(baseURL+"/order", "application/json", bytes.NewBufferString(body))
	if err != nil {
		log.Printf("Error: %v", err)
		return
	}
	resp.Body.Close()
	log.Printf("Order sent: %s x%d -> %d", productID, quantity, resp.StatusCode)
}

func main() {
	baseURL := os.Getenv("ORDER_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://order-service:8080"
	}

	client := &http.Client{Timeout: 5 * time.Second}

	log.Println("Traffic generator started. Targeting:", baseURL)

	// Wait for services to be ready
	time.Sleep(5 * time.Second)

	for {
		// Simulate realistic traffic patterns
		// Base: 3-8 concurrent goroutines sending requests
		burstSize := rand.Intn(6) + 3
		for i := 0; i < burstSize; i++ {
			go sendOrder(client, baseURL)
		}

		// Variable inter-burst delay (50-200ms) for dense graphs
		delay := time.Duration(rand.Intn(150)+50) * time.Millisecond

		// Occasional traffic spike (every ~30 seconds, send a burst of 20-50 orders)
		if rand.Intn(300) < 1 {
			log.Println(">>> TRAFFIC SPIKE <<<")
			spikeSize := rand.Intn(30) + 20
			for i := 0; i < spikeSize; i++ {
				go sendOrder(client, baseURL)
			}
			delay = 10 * time.Millisecond
		}

		time.Sleep(delay)
	}
}
