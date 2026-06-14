package main

import (
	"flag"
	"log"
	"os"
	"strconv"
)

func main() {
	port := flag.String("port", getEnv("SCHEDULER_PORT", "50053"), "Service port")
	lambda := flag.Float64("lambda", getEnvFloat("SCHEDULER_LAMBDA", 0.5), "Default stability lambda (0.0 - 2.0)")
	flag.Parse()

	solver := NewSolverAdapter()
	solver.SetStabilityLambda(*lambda)

	server := NewServer(solver, *port)

	log.Println("========================================")
	log.Println("  Scheduler Optimizer Service")
	log.Println("  Integer Programming (Standalone)")
	log.Println("========================================")
	log.Printf("  Port:              %s", *port)
	log.Printf("  Stability Lambda:  %.4f", solver.GetStabilityLambda())
	log.Printf("  Default Timeout:   5000ms")
	log.Println("========================================")

	if err := server.Start(); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value, exists := os.LookupEnv(key); exists {
		if v, err := strconv.ParseFloat(value, 64); err == nil {
			return v
		}
	}
	return defaultValue
}
