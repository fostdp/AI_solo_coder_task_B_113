package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
)

func getEnv(key, defaultValue string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	vStr := getEnv(key, "")
	if vStr == "" {
		return defaultValue
	}
	v, err := strconv.Atoi(vStr)
	if err != nil {
		return defaultValue
	}
	return v
}

func main() {
	rpcPort := getEnv("VAP_COX_RPC_PORT", defaultRPCPort)
	healthPort := getEnv("VAP_COX_HEALTH_PORT", defaultHealthPort)
	timeoutSec := getEnvInt("VAP_COX_TIMEOUT_SEC", defaultTimeoutSec)
	poolSize := getEnvInt("VAP_COX_POOL_SIZE", runtime.NumCPU())

	if poolSize <= 0 {
		poolSize = 4
	}

	rpcAddr := fmt.Sprintf(":%s", rpcPort)
	healthAddr := fmt.Sprintf(":%s", healthPort)

	fmt.Printf("=== VAP Cox gRPC Service ===\n")
	fmt.Printf("RPC address:    %s\n", rpcAddr)
	fmt.Printf("Health address: %s\n", healthAddr)
	fmt.Printf("Timeout:        %ds\n", timeoutSec)
	fmt.Printf("Pool size:      %d\n", poolSize)
	fmt.Printf("============================\n")

	server := NewVapCoxServer(rpcAddr, healthAddr, timeoutSec, poolSize)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.Start()
	}()

	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down (context cancelled)...")
	case sig := <-sigCh:
		fmt.Printf("\nReceived signal: %v, shutting down...\n", sig)
	case err := <-serverErrCh:
		if err != nil {
			fmt.Printf("Server error: %v\n", err)
		}
	}

	cancel()
	server.Stop()
	fmt.Println("VAP Cox service stopped gracefully")
}
