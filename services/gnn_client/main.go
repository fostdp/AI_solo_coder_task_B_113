package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	var (
		port             string
		pythonServiceURL string
		numBeds          int
		numFeatures      int
	)

	flag.StringVar(&port, "port", getEnv("GNN_CLIENT_PORT", "50052"), "服务监听端口")
	flag.StringVar(&pythonServiceURL, "python-url", getEnv("GNN_PYTHON_URL", "http://localhost:8000"), "Python GNN服务URL")
	flag.IntVar(&numBeds, "num-beds", getEnvInt("GNN_NUM_BEDS", 50), "床位数量")
	flag.IntVar(&numFeatures, "num-features", getEnvInt("GNN_NUM_FEATURES", 5), "节点特征维度")
	flag.Parse()

	log.Println("============================================")
	log.Println("  GNN Client 独立服务启动")
	log.Println("============================================")
	log.Printf("  监听端口:         %s", port)
	log.Printf("  Python GNN 服务:  %s", pythonServiceURL)
	log.Printf("  床位数量:         %d", numBeds)
	log.Printf("  特征维度:         %d", numFeatures)
	log.Println("============================================")

	predictor := NewPredictorAdapter(pythonServiceURL, numBeds, numFeatures)
	server := NewGnnServer(predictor)

	serverErrors := make(chan error, 1)
	go func() {
		addr := fmt.Sprintf(":%s", port)
		serverErrors <- server.Start(addr)
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		log.Fatalf("服务启动失败: %v", err)

	case sig := <-shutdown:
		log.Printf("收到终止信号: %v", sig)
		log.Println("开始优雅关闭...")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("服务关闭异常: %v", err)
		}

		log.Println("GNN Client 服务已安全关闭")
	}
}

func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
