package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"time"
)

func main() {
	rand.Seed(time.Now().UnixNano())

	var (
		port      int
		nTrees    int
		maxDepth  int
		runClient bool
	)

	flag.IntVar(&port, "port", 50054, "Server port")
	flag.IntVar(&nTrees, "trees", 50, "Number of decision trees in random forest")
	flag.IntVar(&maxDepth, "depth", 3, "Max depth of each decision tree")
	flag.BoolVar(&runClient, "client", false, "Run client demo mode instead of server")
	flag.Parse()

	if envPort := os.Getenv("TRANSPORT_PORT"); envPort != "" {
		fmt.Sscanf(envPort, "%d", &port)
	}
	if envTrees := os.Getenv("FOREST_TREES"); envTrees != "" {
		fmt.Sscanf(envTrees, "%d", &nTrees)
	}
	if envDepth := os.Getenv("FOREST_DEPTH"); envDepth != "" {
		fmt.Sscanf(envDepth, "%d", &maxDepth)
	}

	scorer := NewScorerAdapter(nTrees, maxDepth)

	if runClient {
		runClientDemo(fmt.Sprintf("localhost:%d", port))
		return
	}

	addr := fmt.Sprintf(":%d", port)
	server := NewTransportServer(scorer, addr)

	log.Printf("Starting Transport Risk Scorer Service")
	log.Printf("  Port: %d", port)
	log.Printf("  Forest trees: %d", nTrees)
	log.Printf("  Max depth: %d", maxDepth)
	log.Printf("  Default distance: %.0fm", DefaultDistance)
	log.Printf("  Default vital stability: %.0f", DefaultVitalStability)
	log.Printf("  Default infection risk: %.1f", DefaultInfectionRisk)

	if err := server.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func runClientDemo(serviceAddr string) {
	client := NewTransportClient(serviceAddr)

	log.Printf("Running client demo against %s", serviceAddr)

	req := ScoreRequest{
		RequestID:      1,
		BedID:          101,
		FromBedID:      101,
		ToBedID:        202,
		Distance:       1500,
		Urgent:         true,
		Priority:       3,
		HourOfDay:      14,
		PatientAge:     65,
		VitalStability: 55.0,
		InfectionRisk:  0.8,
	}

	log.Printf("Sending normal request...")
	resp, err := client.Score(req)
	if err != nil {
		log.Printf("Error: %v", err)
	} else {
		log.Printf("Response: request_id=%d score=%d level=%s prob=%.3f",
			resp.RequestID, resp.RiskScore, resp.RiskLevel, resp.AdverseEventProb)
		log.Printf("  Used defaults: distance=%v vitals=%v", resp.UsedDefaultDist, resp.UsedDefaultVitals)
		log.Printf("  Recommendations: %v", resp.Recommendations)
	}

	gpsLostReq := ScoreRequest{
		RequestID:      2,
		BedID:          103,
		FromBedID:      103,
		ToBedID:        305,
		Distance:       -1,
		Urgent:         false,
		Priority:       2,
		HourOfDay:      3,
		PatientAge:     45,
		VitalStability: 80.0,
		InfectionRisk:  0.2,
	}

	log.Printf("Sending GPS-lost request (distance=-1)...")
	resp2, err := client.Score(gpsLostReq)
	if err != nil {
		log.Printf("Error: %v", err)
	} else {
		log.Printf("Response: request_id=%d score=%d level=%s",
			resp2.RequestID, resp2.RiskScore, resp2.RiskLevel)
		log.Printf("  Used defaults: distance=%v vitals=%v", resp2.UsedDefaultDist, resp2.UsedDefaultVitals)
	}

	missingVitalsReq := ScoreRequest{
		RequestID:      3,
		BedID:          105,
		FromBedID:      105,
		ToBedID:        401,
		Distance:       800,
		Urgent:         true,
		Priority:       1,
		HourOfDay:      9,
		PatientAge:     72,
		VitalStability: -1,
		InfectionRisk:  -1,
	}

	log.Printf("Sending missing-vitals request (vital=-1, infection=-1)...")
	resp3, err := client.Score(missingVitalsReq)
	if err != nil {
		log.Printf("Error: %v", err)
	} else {
		log.Printf("Response: request_id=%d score=%d level=%s",
			resp3.RequestID, resp3.RiskScore, resp3.RiskLevel)
		log.Printf("  Used defaults: distance=%v vitals=%v", resp3.UsedDefaultDist, resp3.UsedDefaultVitals)
	}

	log.Printf("Getting stats...")
	stats, _ := client.GetStats()
	log.Printf("Stats: total=%d avg=%.2f levels=%v",
		stats.TotalScores, stats.AvgScore, stats.LevelCounts)
}
